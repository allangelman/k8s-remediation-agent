# k8s-remediation-agent

An autonomous remediation agent for Kubernetes. Faults are injected into a local
[kind](https://kind.sigs.k8s.io/) cluster, and the agent detects, diagnoses,
remediates, verifies recovery, and escalates to a human when it can't fix the
problem. Every action passes through guardrails and is written to an audit log,
and an eval harness measures how often the agent gets it right.

> **Status:** early scaffolding. Nothing runs yet.

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
| `cmd/agent/` | CLI entrypoint for the agent                                 |
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

## License

[MIT](LICENSE)
