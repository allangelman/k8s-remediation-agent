# k8s-remediation-agent

An autonomous remediation agent for Kubernetes. Faults are injected into a local
[kind](https://kind.sigs.k8s.io/) cluster, and the agent detects, diagnoses,
remediates, verifies recovery, and escalates to a human when it can't fix the
problem. Every action passes through guardrails and is written to an audit log,
and an eval harness measures how often the agent gets it right.

> **Status:** the agent diagnoses and fixes the bad-image-tag fault end to end, with guardrails and verification. More faults and the eval harness are in progress.

## How it works

The agent is a Go program that calls the Claude API with a set of tool
definitions. The LLM never touches the cluster directly: it only *requests* tool
calls. The agent checks each request against its guardrails, executes it
(`kubectl` / `argocd`), logs it, and feeds the result back.

```
observe → LLM decides → LLM requests tool → guardrails check → execute → audit log → repeat
→ final: "fixed" (recovery verified) or "escalate"
```

## Repo layout

| Path         | Purpose                                                      |
|--------------|--------------------------------------------------------------|
| `infra/`     | kind cluster config, ArgoCD install, optional Prometheus     |
| `scripts/`   | Shared helper scripts used by the Makefile                   |
| `apps/`      | Sample app manifests that ArgoCD syncs from                  |
| `faults/`    | Scripts that break the sample app in specific ways           |
| `agent/`     | Agent loop, tools, guardrails, prompts                       |
| `cmd/agent/` | CLI entrypoint for the agent (`make agent`)                  |
| `evals/`     | Inject fault → run agent → check recovery → record results   |

## Quick start

Prerequisites: Docker, [kind](https://kind.sigs.k8s.io/), `kubectl`, Go.

```sh
make up        # kind cluster + ArgoCD + demo app (~1.5 min)
make status    # ArgoCD sync/health and demo pods
make argocd-ui # ArgoCD UI at http://localhost:8080
make down      # delete the cluster
```

Break things and recover:

```sh
make break-image
make reset
```

| Target        | What it does |
|---------------|--------------|
| `break-image` | **Fault 1, bad image tag.** Resets first, then pushes a commit to `demo-env` that points the Deployment at an image tag that doesn't exist. New pods can't pull the image (`ImagePullBackOff`), the rollout stalls while the old pods keep serving, and ArgoCD reports the app **Synced but Degraded** (~1 min). |
| `reset`       | **Undo any fault.** Force-resets `demo-env` to match `main`, then waits until the app is Synced and Healthy again (~10 s). |

ArgoCD deploys `apps/demo-app/` from the **`demo-env`** branch, not `main`.
`demo-env` acts as the environment: fault scripts push bad commits to it, and
the agent fixes it. `main` only changes through PRs. `make up` and `make reset`
force-reset `demo-env` to match `main`. The cluster is named `remediation`
(override with `CLUSTER=<name>`).

## Running the agent

Requires an Anthropic API key in `ANTHROPIC_API_KEY`.

```sh
make break-image                # break the app
make agent                      # diagnose, fix, and verify
make agent ARGS="--dry-run"     # diagnose and propose a fix without applying it
make reset                      # undo the fault and wait until the app is healthy
```

### What a run does

1. **Observe:** read the ArgoCD app status and turn it into an alert.
2. **Investigate:** Claude calls read-only tools (ArgoCD status, pods, events,
   logs, the Deployment, and the deploy branch's commit history).
3. **Act:** if the evidence shows a recent commit caused the problem, Claude
   requests `revert_commit`. The request passes through the guardrails below
   before anything changes.
4. **Verify:** the code (not the model) waits for ArgoCD to report the app
   Synced and Healthy at the revert commit.
5. **Report:** Claude calls `submit_diagnosis`. The final status is derived
   from what actually happened:

| Status      | Meaning | Exit code |
|-------------|---------|-----------|
| `RESOLVED`  | An action was applied and recovery was verified | 0 |
| `PROPOSED`  | Dry run: a valid action was planned but not applied | 0 |
| `ESCALATED` | The agent couldn't fix it, wasn't confident, or the fix didn't work | 2 |

### Guardrails

Enforced in code, outside the model's control:

| Guardrail | How |
|-----------|-----|
| **Fixed scope** | Cluster, namespace, app, and branch come from config; the model can't choose them. |
| **Action allowlist** | Only `revert_commit` is enabled. Only allowlisted actions are offered to the model, and every call is checked again. |
| **Blast-radius limit** | At most **one** action per incident. If it doesn't work, the agent escalates instead of trying something else. |
| **Action validation** | A revert must be a full SHA, among the last 10 commits on `demo-env`, not a merge, change only `apps/`, and not already reverted (so it's safe to repeat). Validation happens before anything changes. |
| **Safe apply** | The revert happens in a temporary worktree and is pushed without force, so it fails rather than overwriting a commit someone else just pushed. |
| **Dry run** | `--dry-run` runs every check but stops before applying. |
| **Verification** | Success means ArgoCD reports Synced and Healthy at the revert commit within `--verify-timeout` (default 2m), not that the model says it's fixed. |
| **Audit log** | Every step goes to `audit/<timestamp>.jsonl`: model responses, tool calls with full output, guardrail decisions, the action, verification, and the outcome. |

The allowlist and action limit are deliberately not command-line flags:
widening what the agent may do should be a reviewed code change.

Revert commits are authored by `k8s-remediation-agent`, with the reason in the
message, so `git log` shows what the agent did and why:

```
bd7bf28 k8s-remediation-agent: Revert "Pin podinfo image to v6.15.0"
b09bb21 Allan Gelman: Pin podinfo image to v6.15.0
```

### Example run (bad image tag)

```
ALERT: ArgoCD reports application "demo-app" (namespace "demo") as sync=Synced, health=Degraded. ...

I'll start by checking the app status, pods, and recent events to see how it's failing.
→ get_app_status
→ list_pods
→ get_events
Image pull failure on the new ReplicaSet; checking the pod details and commit history.
→ describe_pod {"name":"demo-app-66d66dd598-hqhv8"}
→ get_deploy_history
Commit b09bb21 changed the image tag from `6.15.0` to `v6.15.0`. The old pods on the previous tag are healthy and the new tag can't be pulled, so I'm reverting that commit.
⚡ revert_commit {"commit_sha":"b09bb21f...","reason":"..."}
  🛡 allowed: revert b09bb21 ("Pin podinfo image to v6.15.0") on demo-env
  applied: pushed bd7bf28
  verifying: waiting up to 2m0s for Synced/Healthy at bd7bf28...
  ✓ recovered: Synced/Healthy at bd7bf28

=== RESOLVED ===
Why:            action applied and recovery verified
Action:         revert b09bb21 ("Pin podinfo image to v6.15.0") on demo-env
Category:       image_pull (confidence: high)
```

Flags: `--dry-run`, `--model` (default `claude-opus-5-5`), `--effort` (default
`medium`), `--max-steps` (default 15), `--verify-timeout` (default `2m`),
`--force` (run even if the app is healthy). Pass them through make with
`ARGS`, e.g. `make agent ARGS="--dry-run --model claude-sonnet-5-5"`.

## License

[MIT](LICENSE)
