package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// Drift is a live object that no longer matches Git, e.g. after someone ran
// `kubectl edit`. ArgoCD reports it as OutOfSync, but not who changed what.
// Kubernetes records that in each object's metadata.managedFields: for every
// field, the tool (field manager) that last wrote it, and when. An on-call
// engineer reads it to tell a hand edit from a bad deploy; get_drift gives the
// agent the same view.

// Field managers that are expected to write the app's objects. Fields they own
// aren't drift.
var systemManagers = []string{
	"argocd-controller",       // ArgoCD applying Git
	"kube-controller-manager", // the Deployment controller (status, revision annotation)
}

// LiveChange is one field of a live object that was last written by something
// other than ArgoCD or Kubernetes itself.
type LiveChange struct {
	Resource    string     // e.g. "Deployment/demo-app"
	Field       string     // e.g. "spec.template.spec.containers[name=podinfo].resources.limits.memory"
	Value       string     // the live value, as JSON
	Manager     string     // field manager, e.g. "kubectl-edit"
	Subresource string     // set when written through a subresource, e.g. "scale"
	Time        *time.Time // when the manager last wrote; nil if not recorded (e.g. kubectl scale)
}

// ByPerson reports whether a person most likely made the change: kubectl
// records field managers starting with "kubectl" (kubectl-edit, kubectl-set,
// kubectl-client-side-apply, and plain "kubectl" for scale). Other managers
// are controllers, scripts, or other automation.
func (c LiveChange) ByPerson() bool { return strings.HasPrefix(c.Manager, "kubectl") }

// Describe formats the change for the model, relative to now.
func (c LiveChange) Describe(now time.Time) string {
	by := fmt.Sprintf("written by %q", c.Manager)
	if c.Subresource != "" {
		by += " via the " + c.Subresource + " subresource"
	}
	when := "time not recorded"
	if c.Time != nil {
		when = ago(now.Sub(*c.Time)) + " (" + c.Time.UTC().Format(time.RFC3339) + ")"
	}
	who := "automation, not kubectl"
	if c.ByPerson() {
		who = "kubectl, so most likely a person"
	}
	return fmt.Sprintf("%s = %s\n      %s, %s; %s", c.Field, c.Value, by, when, who)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// liveChanges reads an object's managedFields and returns every field owned
// by a manager outside systemManagers, with its live value.
func liveChanges(resource string, obj []byte) ([]LiveChange, error) {
	var o struct {
		Metadata struct {
			ManagedFields []struct {
				Manager     string         `json:"manager"`
				Subresource string         `json:"subresource"`
				Time        *time.Time     `json:"time"`
				FieldsV1    map[string]any `json:"fieldsV1"`
			} `json:"managedFields"`
		} `json:"metadata"`
	}
	var live any
	if err := json.Unmarshal(obj, &o); err != nil {
		return nil, fmt.Errorf("parse %s: %w", resource, err)
	}
	if err := json.Unmarshal(obj, &live); err != nil {
		return nil, fmt.Errorf("parse %s: %w", resource, err)
	}

	var changes []LiveChange
	for _, mf := range o.Metadata.ManagedFields {
		if slices.Contains(systemManagers, mf.Manager) || mf.Subresource == "status" {
			continue
		}
		for _, f := range ownedFields(mf.FieldsV1, "", live) {
			v, _ := json.Marshal(f.Value)
			changes = append(changes, LiveChange{
				Resource: resource, Field: f.Path, Value: string(v),
				Manager: mf.Manager, Subresource: mf.Subresource, Time: mf.Time,
			})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Field < changes[j].Field })
	return changes, nil
}

// ownedField is one field a manager owns: its path and its live value.
type ownedField struct {
	Path  string // e.g. "spec.template.spec.containers[name=podinfo].image"
	Value any    // the live value, or nil if the live object doesn't have it
}

// ownedFields walks a managedFields fieldsV1 tree (the fields one manager
// owns) alongside the live object, and returns each owned leaf field with its
// live value. In fieldsV1, "f:<name>" is a field, "k:{...}" is a list item
// picked by its keys (e.g. a container by name), "v:<value>" is a set member,
// and "." means the node itself is owned. An owned list item (e.g. an env var
// someone added) is returned as one field rather than one per subfield.
func ownedFields(fields map[string]any, path string, live any) []ownedField {
	var out []ownedField
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var p string
		var v any
		switch {
		case strings.HasPrefix(key, "f:"):
			name := key[2:]
			p = name
			if path != "" {
				p = path + "." + name
			}
			if m, ok := live.(map[string]any); ok {
				v = m[name]
			}
		case strings.HasPrefix(key, "k:"):
			var sel map[string]any
			if json.Unmarshal([]byte(key[2:]), &sel) != nil {
				continue
			}
			p = path + "[" + selector(sel) + "]"
			v = findItem(live, sel)
		case strings.HasPrefix(key, "v:"):
			p = path + "[" + key[2:] + "]"
			v = json.RawMessage(key[2:])
		default: // "." and anything unrecognized
			continue
		}
		child, _ := fields[key].(map[string]any)
		_, ownsNode := child["."]
		if len(child) == 0 || (ownsNode && (len(child) == 1 || strings.HasPrefix(key, "k:"))) {
			out = append(out, ownedField{p, v})
			continue
		}
		out = append(out, ownedFields(child, p, v)...)
	}
	return out
}

// selector formats list-item keys like name=podinfo, sorted for stable output.
func selector(sel map[string]any) string {
	var parts []string
	for k, v := range sel {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// findItem returns the element of a live list whose fields match sel.
func findItem(live any, sel map[string]any) any {
	items, _ := live.([]any)
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		match := true
		for k, v := range sel {
			if !reflect.DeepEqual(m[k], v) {
				match = false
				break
			}
		}
		if match {
			return item
		}
	}
	return nil
}

// GetLiveChanges returns the live changes on every resource ArgoCD manages in
// the app's namespace. The resources come from ArgoCD, not from the model.
func (c Config) GetLiveChanges(ctx context.Context, resources []ResourceStatus) ([]LiveChange, error) {
	var all []LiveChange
	for _, r := range resources {
		if r.Namespace != c.Namespace {
			continue
		}
		name := r.Kind + "/" + r.Name
		obj, err := c.kubectl(ctx, "-n", c.Namespace, "get", name, "-o", "json", "--show-managed-fields")
		if err != nil {
			return nil, err
		}
		changes, err := liveChanges(name, []byte(obj))
		if err != nil {
			return nil, err
		}
		all = append(all, changes...)
	}
	return all, nil
}

// diffAgainstGit returns ArgoCD's own diff of the live objects against Git:
// the same diff the ArgoCD UI shows, and the one ArgoCD uses to decide the app
// is OutOfSync. `argocd app diff --core` computes it from the cluster directly,
// with no ArgoCD login.
func (c Config) diffAgainstGit(ctx context.Context) (string, error) {
	kubeconfig, cleanup, err := c.argocdKubeconfig(ctx)
	if err != nil {
		return "", err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "argocd", "app", "diff", c.App, "--core")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	out, err := cmd.CombinedOutput()
	// argocd app diff exits 1 when there are differences, and 2 on errors.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return string(out), nil
	}
	if err != nil {
		return "", fmt.Errorf("argocd app diff (needs the argocd CLI): %w: %s", err, out)
	}
	return "", nil
}

// argocdKubeconfig returns a KUBECONFIG value for running the argocd CLI in
// core mode against cfg's context. Core mode reads ArgoCD's settings from the
// context's namespace and has no flag for it, so this writes a small kubeconfig
// with one context: cfg's cluster and user, with the namespace set to argocd.
// It's listed first in KUBECONFIG, ahead of the user's own kubeconfig, which
// supplies the cluster and user entries. So the temporary file holds names
// only, never credentials. cleanup removes it.
func (c Config) argocdKubeconfig(ctx context.Context) (kubeconfig string, cleanup func(), err error) {
	// Names of the cluster and user entries behind cfg's context. Without
	// --raw, kubectl redacts credentials, though only names are read here.
	names, err := c.kubectl(ctx, "config", "view", "--minify", "-o",
		"jsonpath={.contexts[0].name} {.contexts[0].context.cluster} {.contexts[0].context.user}")
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(names)
	if len(fields) != 3 || fields[0] != c.KubeContext {
		return "", nil, fmt.Errorf("can't find kube context %q (got %q)", c.KubeContext, names)
	}
	// JSON is valid YAML, so this is a kubeconfig file.
	overlay, _ := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Config", "current-context": "k8s-remediation-agent-argocd",
		"contexts": []map[string]any{{"name": "k8s-remediation-agent-argocd", "context": map[string]string{
			"cluster": fields[1], "user": fields[2], "namespace": "argocd",
		}}},
	})
	f, err := os.CreateTemp("", "agent-kubeconfig-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.Remove(f.Name()) }
	_, err = f.Write(overlay)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}

	base := os.Getenv("KUBECONFIG")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			cleanup()
			return "", nil, err
		}
		base = filepath.Join(home, ".kube", "config")
	}
	return f.Name() + string(os.PathListSeparator) + base, cleanup, nil
}

// driftReport is the get_drift tool's output: ArgoCD's view, who changed
// what (managedFields), and the diff against Git.
func (c Config) driftReport(ctx context.Context) (string, error) {
	st, err := c.GetAppStatus(ctx)
	if err != nil {
		return "", err
	}
	changes, err := c.GetLiveChanges(ctx, st.Resources)
	if err != nil {
		return "", err
	}
	diff, err := c.diffAgainstGit(ctx)
	if err != nil {
		return "", err
	}
	return formatDriftReport(st, changes, diff, time.Now()), nil
}

// formatDriftReport formats get_drift's output. It's separate from
// driftReport, which gathers the inputs from the cluster, so it can be tested.
func formatDriftReport(st AppStatus, changes []LiveChange, diff string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ArgoCD: sync=%s, health=%s, comparing against Git revision %s\n", st.Sync, st.Health, st.Revision)
	for _, r := range st.Resources {
		fmt.Fprintf(&b, "  %s/%s: %s\n", r.Kind, r.Name, r.Status)
	}

	b.WriteString("\nLive fields last written by something other than ArgoCD (from metadata.managedFields):\n")
	if len(changes) == 0 {
		b.WriteString("  none: every field is owned by ArgoCD or Kubernetes' own controllers.\n")
	}
	resource := ""
	for _, ch := range changes {
		if ch.Resource != resource {
			resource = ch.Resource
			fmt.Fprintf(&b, "  %s\n", resource)
		}
		fmt.Fprintf(&b, "    %s\n", ch.Describe(now))
	}

	b.WriteString("\nArgoCD's diff of live objects against Git ('<' is live, '>' is what Git would set):\n")
	if diff == "" {
		b.WriteString("  no differences.\n")
	} else {
		b.WriteString(diff)
	}
	b.WriteString("\nArgoCD only compares fields that Git declares, so a field added live (e.g. a new env var) " +
		"can be listed above while ArgoCD still reports Synced.\n")
	return b.String()
}
