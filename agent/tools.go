package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool is one capability the LLM can request. Run receives the raw JSON input
// from the model and returns text to send back as the tool result.
type Tool struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}

// submitDiagnosisTool is handled by the loop itself: calling it ends the run.
const submitDiagnosisTool = "submit_diagnosis"

// Kubernetes object names: lowercase alphanumerics and '-'. Validating names
// keeps the model from passing flags or other surprises to kubectl.
var k8sName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,251}[a-z0-9])?$`)

func parseName(input json.RawMessage) (string, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if !k8sName.MatchString(in.Name) {
		return "", fmt.Errorf("invalid name %q", in.Name)
	}
	return in.Name, nil
}

var nameProp = map[string]any{"name": map[string]any{"type": "string", "description": "Object name, as shown by list_pods."}}

// readOnlyTools returns the investigation tools. None of them can change the
// cluster or the repo; every kubectl call is pinned to cfg's context and namespace.
func readOnlyTools(cfg Config) []Tool {
	ns := cfg.Namespace
	return []Tool{
		{
			Name:        "get_app_status",
			Description: "ArgoCD sync status, health status, current Git revision, and the last few deploys for the app.",
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				st, err := cfg.GetAppStatus(ctx)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(st, "", "  ")
				return string(b), nil
			},
		},
		{
			Name:        "list_pods",
			Description: "List pods in the app's namespace with status, restarts, and age.",
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return cfg.kubectl(ctx, "-n", ns, "get", "pods", "-o", "wide")
			},
		},
		{
			Name:        "describe_pod",
			Description: "kubectl describe for one pod: container state, image, probes, resource limits, and recent events.",
			Properties:  nameProp,
			Required:    []string{"name"},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				name, err := parseName(input)
				if err != nil {
					return "", err
				}
				return cfg.kubectl(ctx, "-n", ns, "describe", "pod", name)
			},
		},
		{
			Name:        "describe_deployment",
			Description: "kubectl describe for the app's Deployment: rollout status, replica counts, pod template, and conditions.",
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return cfg.kubectl(ctx, "-n", ns, "describe", "deployment", cfg.App)
			},
		},
		{
			Name:        "get_events",
			Description: "Recent Kubernetes events in the app's namespace, oldest first.",
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return cfg.kubectl(ctx, "-n", ns, "get", "events", "--sort-by=.lastTimestamp")
			},
		},
		{
			Name:        "get_pod_logs",
			Description: "Last 100 log lines from a pod. Set previous=true to read the previous (crashed) container's logs.",
			Properties: map[string]any{
				"name":     nameProp["name"],
				"previous": map[string]any{"type": "boolean", "description": "Read the previous container instance's logs."},
			},
			Required: []string{"name"},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				name, err := parseName(input)
				if err != nil {
					return "", err
				}
				var in struct {
					Previous bool `json:"previous"`
				}
				_ = json.Unmarshal(input, &in)
				args := []string{"-n", ns, "logs", name, "--tail=100", "--all-containers"}
				if in.Previous {
					args = append(args, "--previous")
				}
				return cfg.kubectl(ctx, args...)
			},
		},
		{
			Name: "get_deploy_history",
			Description: "Recent commits to the app's manifests on the branch ArgoCD deploys from, newest first, " +
				"with each commit's diff. Use this to find which change caused a problem.",
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				if _, err := run(ctx, "git", "-C", cfg.RepoDir, "fetch", "-q", "origin", cfg.EnvBranch); err != nil {
					return "", err
				}
				return run(ctx, "git", "-C", cfg.RepoDir, "log", "-n", "5", "-p",
					"--format=commit %H%nAuthor: %an%nDate:   %ar%n%n    %s%n",
					"origin/"+cfg.EnvBranch, "--", "apps/")
			},
		},
	}
}

// diagnosisToolParam defines submit_diagnosis, the structured end of every run.
// strict mode guarantees the input matches this schema exactly.
func diagnosisToolParam() anthropic.BetaToolParam {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	enum := func(desc string, vals ...string) map[string]any {
		return map[string]any{"type": "string", "enum": vals, "description": desc}
	}
	props := map[string]any{
		"category": enum("The kind of failure.",
			"image_pull", "crash_loop", "oom_killed", "readiness_probe", "config_drift", "other", "unknown"),
		"root_cause":     str("One or two sentences: what is broken and why."),
		"evidence":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Specific observations that support the root cause (events, statuses, log lines, diffs)."},
		"suspect_commit": str("Full SHA of the commit that introduced the problem, or an empty string if no commit is responsible."),
		"proposed_fix":   str("The remediation you would take, e.g. revert a specific commit. Describe it; do not perform it."),
		"confidence":     enum("How sure you are of the root cause.", "low", "medium", "high"),
		"needs_human":    map[string]any{"type": "boolean", "description": "True if this should be escalated to a human instead of fixed automatically."},
	}
	return anthropic.BetaToolParam{
		Name:        submitDiagnosisTool,
		Description: anthropic.String("Submit your final diagnosis. Call this exactly once, when your investigation is complete. It ends the run."),
		Strict:      anthropic.Bool(true),
		InputSchema: anthropic.BetaToolInputSchemaParam{
			Properties:  props,
			Required:    []string{"category", "root_cause", "evidence", "suspect_commit", "proposed_fix", "confidence", "needs_human"},
			ExtraFields: map[string]any{"additionalProperties": false},
		},
	}
}

// Diagnosis is the parsed submit_diagnosis input.
type Diagnosis struct {
	Category      string   `json:"category"`
	RootCause     string   `json:"root_cause"`
	Evidence      []string `json:"evidence"`
	SuspectCommit string   `json:"suspect_commit"`
	ProposedFix   string   `json:"proposed_fix"`
	Confidence    string   `json:"confidence"`
	NeedsHuman    bool     `json:"needs_human"`
}
