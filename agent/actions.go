package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// Action is a tool that changes something. Unlike read-only tools, an action
// is split in two: Plan validates the request without changing anything, and
// the returned Plan's Apply makes the change. Dry-run mode stops after Plan.
type Action struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Plan        func(ctx context.Context, input json.RawMessage) (*Plan, error)
}

// Plan is a validated action, ready to apply.
type Plan struct {
	Summary string // human-readable description of the change
	Target  string // what the action acts on, e.g. the commit being reverted
	// Apply makes the change and returns the Git revision ArgoCD should end up
	// on, which verification waits for.
	Apply func(ctx context.Context) (revision string, err error)
}

// actions returns every action the agent knows how to take. Only the ones in
// Config.AllowedActions are offered to the model.
func actions(cfg Config) []Action {
	return []Action{revertCommitAction(cfg)}
}

func revertCommitAction(cfg Config) Action {
	return Action{
		Name: "revert_commit",
		Description: "Revert one commit on the branch ArgoCD deploys from and push it; ArgoCD then deploys the reverted state. " +
			"Use this only when the evidence shows that commit caused the problem. The commit must be recent and only " +
			"change app manifests. After the revert, the system waits for the app to recover and reports the result. " +
			"You may take at most one action per incident.",
		Properties: map[string]any{
			"commit_sha": map[string]any{"type": "string", "description": "Full 40-character SHA of the commit to revert."},
			"reason":     map[string]any{"type": "string", "description": "One sentence on why this commit caused the problem. Recorded in the revert commit message."},
		},
		Required: []string{"commit_sha", "reason"},
		Plan: func(ctx context.Context, input json.RawMessage) (*Plan, error) {
			var in struct {
				CommitSHA string `json:"commit_sha"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if in.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			if _, err := git(ctx, cfg.RepoDir, "fetch", "-q", "origin", cfg.EnvBranch); err != nil {
				return nil, err
			}
			subject, err := validatePotentialRevert(ctx, cfg.RepoDir, "origin/"+cfg.EnvBranch, in.CommitSHA)
			if err != nil {
				return nil, err
			}
			return &Plan{
				Summary: fmt.Sprintf("revert %s (%q) on %s", in.CommitSHA[:7], subject, cfg.EnvBranch),
				Target:  in.CommitSHA,
				Apply: func(ctx context.Context) (string, error) {
					rev, err := revertAndPush(ctx, cfg, in.CommitSHA, subject, in.Reason)
					if err != nil {
						return "", err
					}
					// Ask ArgoCD to pick up the new commit now instead of on its next poll.
					return rev, cfg.Refresh(ctx)
				},
			}, nil
		},
	}
}
