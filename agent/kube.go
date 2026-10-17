package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// run executes a command and returns its combined output. Commands are run
// directly (no shell), so tool inputs can't inject extra commands.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func (c Config) kubectl(ctx context.Context, args ...string) (string, error) {
	return run(ctx, "kubectl", append([]string{"--context", c.KubeContext}, args...)...)
}

// AppStatus is the slice of the ArgoCD Application status the agent cares about.
type AppStatus struct {
	Sync     string `json:"sync"`
	Revision string `json:"revision"`
	Health   string `json:"health"`
	Message  string `json:"message,omitempty"`
	History  []struct {
		Revision   string `json:"revision"`
		DeployedAt string `json:"deployedAt"`
	} `json:"history,omitempty"`
}

// GetAppStatus reads the ArgoCD Application and trims it to sync, health, and
// recent deploy history (the full object is mostly noise for diagnosis).
func (c Config) GetAppStatus(ctx context.Context) (AppStatus, error) {
	out, err := c.kubectl(ctx, "-n", "argocd", "get", "application", c.App, "-o", "json")
	if err != nil {
		return AppStatus{}, err
	}
	var app struct {
		Status struct {
			Sync struct {
				Status   string `json:"status"`
				Revision string `json:"revision"`
			} `json:"sync"`
			Health struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
			History []struct {
				Revision   string `json:"revision"`
				DeployedAt string `json:"deployedAt"`
			} `json:"history"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &app); err != nil {
		return AppStatus{}, fmt.Errorf("parse application: %w", err)
	}
	s := app.Status
	st := AppStatus{Sync: s.Sync.Status, Revision: s.Sync.Revision, Health: s.Health.Status, Message: s.Health.Message}
	// Keep the 5 most recent deploys.
	if n := len(s.History); n > 5 {
		s.History = s.History[n-5:]
	}
	st.History = s.History
	return st, nil
}
