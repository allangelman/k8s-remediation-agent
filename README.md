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
| `apps/`      | Sample app manifests that ArgoCD syncs from                  |
| `faults/`    | Scripts that break the sample app in specific ways           |
| `agent/`     | Agent loop, tools, guardrails, prompts                       |
| `cmd/agent/` | CLI entrypoint for the agent                                 |
| `evals/`     | Inject fault → run agent → check recovery → record results   |

## Quick start

```sh
make help
```

Cluster setup (`make up`), fault injection, and the eval harness are coming in
follow-up PRs.

## License

[MIT](LICENSE)
