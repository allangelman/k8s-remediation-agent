package agent

import (
	"fmt"
	"slices"
)

// Guardrails decide whether a mutating action may run. They are enforced by
// this code, not by the prompt: the model can ask for anything, but only
// requests that pass these checks reach the cluster or the repo.
type Guardrails struct {
	allowed    []string
	maxActions int
	used       int
}

func NewGuardrails(cfg Config) *Guardrails {
	return &Guardrails{allowed: cfg.AllowedActions, maxActions: cfg.MaxActions}
}

// Check returns an error if the action isn't allowlisted or the incident's
// action budget is used up.
func (g *Guardrails) Check(action string) error {
	if !slices.Contains(g.allowed, action) {
		return fmt.Errorf("action %q is not on the allowlist %v", action, g.allowed)
	}
	if g.used >= g.maxActions {
		return fmt.Errorf("blast-radius limit reached: %d of %d actions already used for this incident", g.used, g.maxActions)
	}
	return nil
}

// Consume records that an action is going ahead (or, in dry-run mode, was
// proposed), so it counts against the incident's budget.
func (g *Guardrails) Consume() { g.used++ }
