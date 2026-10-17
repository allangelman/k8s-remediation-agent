package agent

import (
	"strings"
	"testing"
)

func TestGuardrails(t *testing.T) {
	g := NewGuardrails(Config{AllowedActions: []string{"revert_commit"}, MaxActions: 1})

	if err := g.Check("restart_deployment"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("non-allowlisted action: got %v, want allowlist error", err)
	}
	if err := g.Check("revert_commit"); err != nil {
		t.Errorf("first revert_commit: got %v, want allowed", err)
	}
	g.RecordAttempt()
	if err := g.Check("revert_commit"); err == nil || !strings.Contains(err.Error(), "blast-radius") {
		t.Errorf("second action: got %v, want blast-radius error", err)
	}
}

func TestDecideOutcome(t *testing.T) {
	diag := &Diagnosis{}
	needsHuman := &Diagnosis{NeedsHuman: true}
	tests := []struct {
		name string
		d    *Diagnosis
		a    *ActionRecord
		want string
	}{
		{"applied and verified", diag, &ActionRecord{Applied: true, Verified: true}, StatusResolved},
		{"verified even if model is unsure", needsHuman, &ActionRecord{Applied: true, Verified: true}, StatusResolved},
		{"applied, not recovered", diag, &ActionRecord{Applied: true, Error: "timeout"}, StatusEscalated},
		{"dry run", diag, &ActionRecord{DryRun: true}, StatusProposed},
		{"apply failed", diag, &ActionRecord{Error: "push rejected"}, StatusEscalated},
		{"model asked for a human", needsHuman, nil, StatusEscalated},
		{"diagnosed, no action", diag, nil, StatusEscalated},
		{"no diagnosis", nil, nil, StatusEscalated},
	}
	for _, tc := range tests {
		if got := decideOutcome(tc.d, tc.a).Status; got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
