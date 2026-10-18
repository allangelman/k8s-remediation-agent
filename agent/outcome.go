package agent

// Outcome statuses.
const (
	StatusResolved  = "resolved"  // an action was applied and the app recovered
	StatusProposed  = "proposed"  // dry run: a valid action was planned but not applied
	StatusEscalated = "escalated" // a human needs to take over
)

// ActionRecord is what happened to the (at most one) action in a run.
type ActionRecord struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Target   string `json:"target"` // e.g. the reverted commit's SHA
	DryRun   bool   `json:"dry_run"`
	Applied  bool   `json:"applied"`
	Revision string `json:"revision,omitempty"` // commit the action pushed
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`
}

// Outcome is the result of a run.
type Outcome struct {
	Status    string        `json:"status"`
	Reason    string        `json:"reason"`
	Diagnosis *Diagnosis    `json:"diagnosis,omitempty"`
	Action    *ActionRecord `json:"action,omitempty"`
}

// decideOutcome derives the run's status from what actually happened. It is
// deliberately independent of the model's own claims: "resolved" requires an
// applied action that verification confirmed.
func decideOutcome(d *Diagnosis, a *ActionRecord) Outcome {
	o := Outcome{Diagnosis: d, Action: a}
	switch {
	case a != nil && a.Applied && a.Verified:
		o.Status, o.Reason = StatusResolved, "action applied and recovery verified"
	case a != nil && a.Applied:
		o.Status, o.Reason = StatusEscalated, "action applied but the app did not recover: "+a.Error
	case a != nil && a.DryRun && a.Error == "":
		o.Status, o.Reason = StatusProposed, "dry run: action validated but not applied"
	case a != nil:
		o.Status, o.Reason = StatusEscalated, "action failed: "+a.Error
	case d == nil:
		o.Status, o.Reason = StatusEscalated, "agent did not reach a diagnosis"
	case d.NeedsHuman:
		o.Status, o.Reason = StatusEscalated, "agent requested a human"
	default:
		o.Status, o.Reason = StatusEscalated, "agent diagnosed the problem but took no action"
	}
	return o
}
