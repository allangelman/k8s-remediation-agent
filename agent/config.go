// Package agent contains the remediation loop: the LLM calls, tool
// definitions, guardrails, and audit logging.
package agent

import "time"

// Config fixes everything the agent is allowed to look at and do. The LLM
// never chooses the cluster, namespace, app, or branch: tools always use these
// values.
type Config struct {
	KubeContext string // kubectl context, e.g. "kind-remediation"
	Namespace   string // namespace the app runs in, e.g. "demo"
	App         string // ArgoCD Application (and Deployment) name, e.g. "demo-app"
	RepoDir     string // local clone of the repo, for deploy history and reverts
	EnvBranch   string // branch ArgoCD deploys from, e.g. "demo-env"

	Model    string // Claude model ID
	Effort   string // low, medium, high, xhigh, or max
	MaxSteps int    // max LLM round trips before giving up

	// Guardrails for mutating actions; see guardrails.go.
	AllowedActions []string      // actions the agent may take; anything else is denied
	MaxActions     int           // blast-radius limit: mutating actions per incident
	DryRun         bool          // validate and log actions, but don't apply them
	VerifyTimeout  time.Duration // how long to wait for recovery after an action

	AuditPath string // JSONL file every step is appended to
}
