// Command agent responds to an unhealthy app on the local kind cluster: it
// diagnoses the problem, takes at most one guarded action to fix it, verifies
// recovery, and escalates if it can't. Exit codes: 0 resolved or proposed (dry
// run), 1 error, 2 escalated to a human.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/allangelman/k8s-remediation-agent/agent"
)

func main() {
	cfg := agent.Config{}
	flag.StringVar(&cfg.KubeContext, "context", "kind-remediation", "kubectl context")
	flag.StringVar(&cfg.Namespace, "namespace", "demo", "namespace the app runs in")
	flag.StringVar(&cfg.App, "app", "demo-app", "ArgoCD Application and Deployment name")
	flag.StringVar(&cfg.RepoDir, "repo", ".", "local clone of this repo")
	flag.StringVar(&cfg.EnvBranch, "env-branch", "demo-env", "branch ArgoCD deploys from")
	flag.StringVar(&cfg.Model, "model", "claude-opus-5-5", "Claude model ID")
	flag.StringVar(&cfg.Effort, "effort", "medium", "effort: low, medium, high, xhigh, or max")
	flag.IntVar(&cfg.MaxSteps, "max-steps", 15, "max LLM round trips before giving up")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "validate and log actions, but don't apply them")
	flag.DurationVar(&cfg.VerifyTimeout, "verify-timeout", 2*time.Minute, "how long to wait for recovery after an action")
	auditDir := flag.String("audit-dir", "audit", "directory for JSONL audit logs")
	force := flag.Bool("force", false, "investigate even if the app is Healthy")
	flag.Parse()

	// Guardrails that aren't flags on purpose: widening them should be a
	// reviewed code change, not a command-line option.
	cfg.AllowedActions = []string{"revert_commit"}
	cfg.MaxActions = 1

	if err := run(cfg, *auditDir, *force); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cfg agent.Config, auditDir string, force bool) error {
	// ctx carries a deadline and a cancel signal into every API call and kubectl
	// command. It's cancelled after 10 minutes or on Ctrl-C, whichever is first.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	status, err := cfg.GetAppStatus(ctx)
	if err != nil {
		return err
	}
	if status.Health == "Healthy" && status.Sync == "Synced" && !force {
		fmt.Println("demo-app is Synced and Healthy; nothing to investigate (use --force to run anyway).")
		return nil
	}

	cfg.AuditPath = filepath.Join(auditDir, time.Now().UTC().Format("20060102T150405Z")+".jsonl")
	audit, err := agent.OpenAuditLog(cfg.AuditPath)
	if err != nil {
		return err
	}
	defer audit.Close()

	o, err := agent.Run(ctx, cfg, anthropic.NewClient(), audit, os.Stdout)
	fmt.Printf("Audit log: %s\n", cfg.AuditPath)
	if err != nil {
		return err
	}
	printOutcome(o)
	if o.Status == agent.StatusEscalated {
		os.Exit(2)
	}
	return nil
}

func printOutcome(o *agent.Outcome) {
	fmt.Printf("\n=== %s ===\n", strings.ToUpper(o.Status))
	fmt.Printf("Why:            %s\n", o.Reason)
	if a := o.Action; a != nil {
		fmt.Printf("Action:         %s\n", a.Summary)
		if a.Revision != "" {
			fmt.Printf("Pushed:         %s\n", a.Revision)
		}
	}
	d := o.Diagnosis
	if d == nil {
		return
	}
	fmt.Printf("Category:       %s (confidence: %s)\n", d.Category, d.Confidence)
	fmt.Printf("Root cause:     %s\n", d.RootCause)
	if d.SuspectCommit != "" {
		fmt.Printf("Suspect commit: %s\n", d.SuspectCommit)
	}
	fmt.Printf("Fix:            %s\n", d.ProposedFix)
	fmt.Printf("Needs human:    %t\n", d.NeedsHuman)
	fmt.Printf("Evidence:\n  - %s\n", strings.Join(d.Evidence, "\n  - "))
}
