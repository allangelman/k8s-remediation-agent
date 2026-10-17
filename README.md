# k8s-remediation-agent

An autonomous remediation agent for Kubernetes. Faults are injected into a local
[kind](https://kind.sigs.k8s.io/) cluster, and the agent detects, diagnoses,
remediates, verifies recovery, and escalates to a human when it can't fix the
problem. Every action passes through guardrails and is written to an audit log,
and an eval harness measures how often the agent gets it right.

> **Status:** the agent diagnoses faults (read-only). Remediation, guardrails, and evals are in progress.

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
| `break-image` | **Fault 1, bad image tag.** Pushes a commit to `demo-env` that points the Deployment at an image tag that doesn't exist. New pods can't pull the image (`ImagePullBackOff`), the rollout stalls while the old pods keep serving, and ArgoCD reports the app **Synced but Degraded** (~1 min). |
| `reset`       | **Undo any fault.** Force-resets `demo-env` to match `main`, then waits until the app is Synced and Healthy again (~10 s). |

ArgoCD deploys `apps/demo-app/` from the **`demo-env`** branch, not `main`.
`demo-env` acts as the environment: fault scripts push bad commits to it, and
the agent fixes it. `main` only changes through PRs. `make up` and `make reset`
force-reset `demo-env` to match `main`. The cluster is named `remediation`
(override with `CLUSTER=<name>`).

## Running the agent

Requires an Anthropic API key in `ANTHROPIC_API_KEY`.

```sh
make break-image  # break the app
make agent        # investigate and print a diagnosis
make reset        # put it back
```

The agent is currently **read-only**: its tools can inspect ArgoCD, pods,
events, logs, the Deployment, and the deploy branch's commit history, but
cannot change anything. Every kubectl call is pinned to one context and
namespace by the code, not chosen by the model, and object names are validated
before they reach kubectl.

A run ends when the model calls `submit_diagnosis` with a structured result
(category, root cause, evidence, suspect commit, proposed fix, confidence,
whether a human is needed). If it can't reach one within `--max-steps`, the
agent escalates. Every step (model responses, tool calls with full output, and
the diagnosis) is written to `audit/<timestamp>.jsonl`.

Example (bad image tag):

```
ALERT: ArgoCD reports application "demo-app" (namespace "demo") as sync=Synced, health=Degraded. ...

I'll start by checking the ArgoCD app status, pods, and recent events in parallel to get an overview.
→ get_app_status
→ list_pods
→ get_events
The new pod can't pull image tag v6.15.0 ("not found"). Next I'll check the commit history to find which change set that tag, and look at the current Deployment.
→ get_deploy_history
→ describe_deployment

=== DIAGNOSIS ===
Category:       image_pull (confidence: high)
Root cause:     Commit 78e5b37 changed the podinfo image tag from '6.15.0' to 'v6.15.0'. That tag doesn't exist in ghcr.io, ...
Suspect commit: 78e5b37e2548a04425c97d6e506b019692014c66
Proposed fix:   Revert commit 78e5b37e... on the ArgoCD-tracked branch ...
```

Flags: `--model` (default `claude-opus-5-5`), `--effort` (default `medium`),
`--max-steps` (default 15), `--force` (run even if the app is healthy).

## License

[MIT](LICENSE)
