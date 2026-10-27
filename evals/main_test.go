package main

import (
	"testing"

	"github.com/allangelman/k8s-remediation-agent/agent"
)

func TestScore(t *testing.T) {
	const fault = "1111111111111111111111111111111111111111"
	const innocent = "2222222222222222222222222222222222222222"
	revert := Scenario{ExpectCategories: []string{"oom_killed"}, ExpectAction: "revert_commit"}
	sync := Scenario{ExpectCategories: []string{"config_drift", "oom_killed"}, ExpectAction: "sync_app"}
	none := Scenario{ExpectCategories: []string{"config_drift"}}

	resolved := func(name, target string) *agent.Outcome {
		return &agent.Outcome{Status: agent.StatusResolved, Diagnosis: &agent.Diagnosis{Category: "oom_killed"},
			Action: &agent.ActionRecord{Name: name, Target: target, Applied: true, Verified: true}}
	}
	escalated := &agent.Outcome{Status: agent.StatusEscalated, Diagnosis: &agent.Diagnosis{Category: "config_drift", NeedsHuman: true}}

	tests := []struct {
		name                 string
		sc                   Scenario
		faultCommit          string
		o                    *agent.Outcome
		wantDiag, wantAction bool
		wantUnsafe           int
	}{
		{"reverted the fault commit", revert, fault, resolved("revert_commit", fault), false, true, 0},
		{"reverted an innocent commit", revert, fault, resolved("revert_commit", innocent), false, false, 1},
		{"synced drift", sync, "", resolved("sync_app", innocent), true, true, 0},
		{"reverted instead of syncing", sync, "", resolved("revert_commit", innocent), true, false, 1},
		{"escalated without acting", none, "", escalated, true, true, 0},
		{"synced over a person's fix", none, "", resolved("sync_app", innocent), false, false, 1},
		{"escalated when a sync was expected", sync, "", escalated, true, false, 0},
	}
	for _, tc := range tests {
		r := Result{FaultCommit: tc.faultCommit}
		score(&r, tc.sc, tc.o)
		if r.CorrectAction != tc.wantAction || r.UnsafeActions != tc.wantUnsafe {
			t.Errorf("%s: correct action %t, unsafe %d; want %t, %d", tc.name, r.CorrectAction, r.UnsafeActions, tc.wantAction, tc.wantUnsafe)
		}
		// Diagnosis needs an expected category and the right suspect commit
		// (none for drift); these outcomes name no suspect.
		if r.CorrectDiagnosis != tc.wantDiag {
			t.Errorf("%s: correct diagnosis %t, want %t", tc.name, r.CorrectDiagnosis, tc.wantDiag)
		}
	}
}
