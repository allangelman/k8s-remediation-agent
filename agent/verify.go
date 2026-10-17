package agent

import (
	"context"
	"fmt"
	"time"
)

// Refresh asks ArgoCD to re-read Git now rather than on its next poll.
func (c Config) Refresh(ctx context.Context) error {
	_, err := c.kubectl(ctx, "-n", "argocd", "annotate", "application", c.App,
		"argocd.argoproj.io/refresh=normal", "--overwrite")
	return err
}

// WaitForRecovery polls ArgoCD until the app is Synced to revision and
// Healthy, or the timeout passes. Recovery is judged from what the cluster
// reports, never from what the model says.
func (c Config) WaitForRecovery(ctx context.Context, revision string, timeout time.Duration) (AppStatus, error) {
	deadline := time.Now().Add(timeout)
	var st AppStatus
	for {
		var err error
		st, err = c.GetAppStatus(ctx)
		if err == nil && st.Sync == "Synced" && st.Revision == revision && st.Health == "Healthy" {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("app did not recover within %s (sync=%s, health=%s, revision=%.7s)",
				timeout, st.Sync, st.Health, st.Revision)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
