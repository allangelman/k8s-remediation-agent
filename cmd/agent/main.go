// Command agent investigates an unhealthy app on the local kind cluster and
// prints a diagnosis. It is read-only: it never changes the cluster or repo.
package main

import (
	"context"
	"errors"
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
	auditDir := flag.String("audit-dir", "audit", "directory for JSONL audit logs")
	force := flag.Bool("force", false, "investigate even if the app is Healthy")
	flag.Parse()

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

	d, err := agent.Run(ctx, cfg, anthropic.NewClient(), audit, os.Stdout)
	fmt.Printf("Audit log: %s\n", cfg.AuditPath)
	if errors.Is(err, agent.ErrNoDiagnosis) {
		fmt.Printf("\nESCALATE: %v\n", err)
		os.Exit(2)
	}
	if err != nil {
		return err
	}

	fmt.Printf("\n=== DIAGNOSIS ===\n")
	fmt.Printf("Category:       %s (confidence: %s)\n", d.Category, d.Confidence)
	fmt.Printf("Root cause:     %s\n", d.RootCause)
	if d.SuspectCommit != "" {
		fmt.Printf("Suspect commit: %s\n", d.SuspectCommit)
	}
	fmt.Printf("Proposed fix:   %s\n", d.ProposedFix)
	fmt.Printf("Needs human:    %t\n", d.NeedsHuman)
	fmt.Printf("Evidence:\n  - %s\n", strings.Join(d.Evidence, "\n  - "))
	return nil
}
