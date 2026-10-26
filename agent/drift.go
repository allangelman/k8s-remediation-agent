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
		walkFields(mf.FieldsV1, "", live, func(field string, value any) {
			v, _ := json.Marshal(value)
			changes = append(changes, LiveChange{
				Resource: resource, Field: field, Value: string(v),
				Manager: mf.Manager, Subresource: mf.Subresource, Time: mf.Time,
			})
		})
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Field < changes[j].Field })
	return changes, nil
}

// walkFields walks a managedFields fieldsV1 tree alongside the live object
// and calls emit for each owned leaf field. In fieldsV1, "f:<name>" is a field,
// "k:{...}" is a list item picked by its keys (e.g. a container by name),
// "v:<value>" is a set member, and "." means the node itself is owned. An
// owned list item (e.g. an env var someone added) is reported as one change.
func walkFields(fields map[string]any, path string, live any, emit func(field string, value any)) {
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
			emit(p, v)
			continue
		}
		walkFields(child, p, v, emit)
	}
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

// diffAgainstGit runs `kubectl diff` with the app's manifests from the deploy
// branch: what applying Git would change on the live objects. kubectl diff is
// read-only (it uses a server-side dry run).
func (c Config) diffAgainstGit(ctx context.Context) (string, error) {
	ref, dir := "origin/"+c.EnvBranch, "apps/"+c.App
	if _, err := git(ctx, c.RepoDir, "fetch", "-q", "origin", c.EnvBranch); err != nil {
		return "", err
	}
	files, err := git(ctx, c.RepoDir, "ls-tree", "--name-only", ref+":"+dir)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "agent-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, f := range strings.Fields(files) {
		if !strings.HasSuffix(f, ".yaml") && !strings.HasSuffix(f, ".yml") {
			continue
		}
		content, err := git(ctx, c.RepoDir, "show", ref+":"+dir+"/"+f)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(tmp, f), []byte(content+"\n"), 0o600); err != nil {
			return "", err
		}
	}

	out, err := exec.CommandContext(ctx, "kubectl", "--context", c.KubeContext, "-n", c.Namespace, "diff", "-f", tmp).CombinedOutput()
	// kubectl diff exits 1 when there are differences, and >1 on errors.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return cleanDiff(string(out)), nil
	}
	if err != nil {
		return "", fmt.Errorf("kubectl diff: %w: %s", err, out)
	}
	return "", nil
}

// cleanDiff trims kubectl diff output for the model: temp-file paths become
// "live <object>" and "git <object>", and the last-applied-configuration
// annotation (a long JSON copy of an earlier apply) is dropped.
func cleanDiff(diff string) string {
	var out []string
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		object := line[strings.LastIndex(line, "/")+1:]
		object, _, _ = strings.Cut(object, "\t")
		switch {
		case strings.HasPrefix(line, "diff -u"):
			continue
		case strings.HasPrefix(line, "--- "):
			out = append(out, "--- live "+object)
		case strings.HasPrefix(line, "+++ "):
			out = append(out, "+++ git "+object)
		case strings.Contains(line, "kubectl.kubernetes.io/last-applied-configuration"):
			i++ // the annotation's value is on the next line
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// driftReport is the get_drift tool's output: ArgoCD's view, who changed
// what (managedFields), and the diff against Git.
func (c Config) driftReport(ctx context.Context) (string, error) {
	st, err := c.GetAppStatus(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ArgoCD: sync=%s, health=%s, comparing against Git revision %s\n", st.Sync, st.Health, st.Revision)
	for _, r := range st.Resources {
		fmt.Fprintf(&b, "  %s/%s: %s\n", r.Kind, r.Name, r.Status)
	}

	changes, err := c.GetLiveChanges(ctx, st.Resources)
	if err != nil {
		return "", err
	}
	b.WriteString("\nLive fields last written by something other than ArgoCD (from metadata.managedFields):\n")
	if len(changes) == 0 {
		b.WriteString("  none: every field is owned by ArgoCD or Kubernetes' own controllers.\n")
	}
	now, resource := time.Now(), ""
	for _, ch := range changes {
		if ch.Resource != resource {
			resource = ch.Resource
			fmt.Fprintf(&b, "  %s\n", resource)
		}
		fmt.Fprintf(&b, "    %s\n", ch.Describe(now))
	}

	diff, err := c.diffAgainstGit(ctx)
	if err != nil {
		return "", err
	}
	b.WriteString("\nDiff of live objects against Git (kubectl diff; '-' is live, '+' is what Git would set):\n")
	if diff == "" {
		b.WriteString("  no differences.\n")
	} else {
		b.WriteString("Note: ArgoCD's tracking-id annotation and metadata.generation always differ; ignore them.\n")
		b.WriteString(diff)
	}
	b.WriteString("\nArgoCD only compares fields that Git declares, so a field added live (e.g. a new env var) " +
		"can be listed above while ArgoCD still reports Synced.\n")
	return b.String(), nil
}
