// Package agent contains the remediation loop: the LLM calls, tool
// definitions, guardrails, and audit logging.
package agent

// Config fixes everything the agent is allowed to look at. The LLM never
// chooses the cluster, namespace, or app: tools always use these values.
type Config struct {
	KubeContext string // kubectl context, e.g. "kind-remediation"
	Namespace   string // namespace the app runs in, e.g. "demo"
	App         string // ArgoCD Application (and Deployment) name, e.g. "demo-app"
	RepoDir     string // local clone of the repo, for reading deploy history
	EnvBranch   string // branch ArgoCD deploys from, e.g. "demo-env"

	Model    string // Claude model ID
	Effort   string // low, medium, high, xhigh, or max
	MaxSteps int    // max LLM round trips before giving up

	AuditPath string // JSONL file every step is appended to
}
