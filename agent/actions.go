package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
	Applied string // what Apply did, for the terminal, e.g. "pushed the revert to demo-env"
	Target  string // what the action acts on, e.g. the commit being reverted
	// Apply makes the change and returns the Git revision ArgoCD should end up
	// on, which verification waits for.
	Apply func(ctx context.Context) (revision string, err error)
}

// actions returns every action the agent knows how to take. Only the ones in
// Config.AllowedActions are offered to the model.
func actions(cfg Config) []Action {
	return []Action{revertCommitAction(cfg), syncAppAction(cfg)}
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
				Applied: "pushed the revert to " + cfg.EnvBranch,
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

func syncAppAction(cfg Config) Action {
	return Action{
		Name: "sync_app",
		Description: "Sync the app with ArgoCD: reapply the manifests from Git at the revision ArgoCD already tracks, " +
			"overwriting changes made to the live objects outside Git. Use this only when live drift caused the problem " +
			"and Git holds the correct state. It is refused if a person changed a live field with kubectl recently, " +
			"or at an unrecorded time, since that may be a deliberate emergency fix. After the sync, the system waits for " +
			"the app to recover and reports the result. You may take at most one action per incident.",
		Properties: map[string]any{
			"reason": map[string]any{"type": "string", "description": "One sentence on why the live drift caused the problem. Recorded on the ArgoCD sync operation."},
		},
		Required: []string{"reason"},
		Plan: func(ctx context.Context, input json.RawMessage) (*Plan, error) {
			var in struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if in.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			st, err := cfg.GetAppStatus(ctx)
			if err != nil {
				return nil, err
			}
			if st.Sync != "OutOfSync" {
				return nil, fmt.Errorf("app is %s, not OutOfSync: there is no drift to sync", st.Sync)
			}
			// Don't start a second operation on top of one in progress (e.g. an
			// auto-sync of a new commit, or a person's sync).
			if st.Operation == "Running" {
				return nil, fmt.Errorf("an ArgoCD operation is already running on the app")
			}
			// The model doesn't pick the revision: the sync goes to whatever
			// ArgoCD is already comparing against.
			if !fullSHA.MatchString(st.Revision) {
				return nil, fmt.Errorf("ArgoCD's target revision %q is not a commit SHA", st.Revision)
			}
			changes, err := cfg.GetLiveChanges(ctx, st.Resources)
			if err != nil {
				return nil, err
			}
			if err := checkPersonEdits(changes, time.Now(), cfg.PersonEditCooldown); err != nil {
				return nil, err
			}
			var fields []string
			for _, c := range changes {
				fields = append(fields, fmt.Sprintf("%s by %s", c.Field, c.Manager))
			}
			overwrites := "no recorded live changes"
			if len(fields) > 0 {
				overwrites = "overwriting " + strings.Join(fields, ", ")
			}
			return &Plan{
				Summary: fmt.Sprintf("sync %s to Git revision %.7s (%s)", cfg.App, st.Revision, overwrites),
				Applied: "started an ArgoCD sync",
				Target:  st.Revision,
				Apply: func(ctx context.Context) (string, error) {
					if err := cfg.Sync(ctx, st.Revision, in.Reason); err != nil {
						return "", err
					}
					return st.Revision, nil
				},
			}, nil
		},
	}
}

// checkPersonEdits refuses to overwrite live fields a person changed with
// kubectl within cooldown, or at an unrecorded time (kubectl scale records
// none). Kubernetes can't say why someone changed a field, and a recent
// manual change during an incident may be the mitigation, so the agent leaves
// it to a human. Changes made by automation aren't blocked.
func checkPersonEdits(changes []LiveChange, now time.Time, cooldown time.Duration) error {
	var blocked []string
	for _, c := range changes {
		switch {
		case !c.ByPerson():
		case c.Time == nil:
			blocked = append(blocked, fmt.Sprintf("%s %s (%s, time not recorded)", c.Resource, c.Field, c.Manager))
		case now.Sub(*c.Time) < cooldown:
			blocked = append(blocked, fmt.Sprintf("%s %s (%s, %s)", c.Resource, c.Field, c.Manager, ago(now.Sub(*c.Time))))
		}
	}
	if len(blocked) > 0 {
		return fmt.Errorf("a person changed these fields with kubectl within the last %s, or at an unrecorded time, "+
			"and a sync would overwrite them: %s. A recent manual change may be a deliberate fix, so it is left to a human",
			cooldown, strings.Join(blocked, "; "))
	}
	return nil
}
