# faults

Scripts that break the sample app in specific, repeatable ways. Each one
pushes a realistic bad commit to the `demo-env` branch (the one ArgoCD deploys
from), so there's real deploy history for the agent to investigate. The old
pods keep serving in every case, but the rollout stalls and ArgoCD reports the
app **Synced but Degraded**.

| Make target   | Script             | Commit                                | Symptom |
|---------------|--------------------|---------------------------------------|---------|
| `break-image` | `bad-image-tag.sh` | "Pin podinfo image to v6.15.0"        | `ImagePullBackOff`: the tag doesn't exist |
| `break-crash` | `crash-loop.sh`    | "Set podinfo listen port explicitly"  | `CrashLoopBackOff`: `--port=98989` is an invalid port |
| `break-oom`   | `oom.sh`           | "Right-size podinfo memory to 8Mi"    | `OOMKilled` (exit 137) on startup, then `CrashLoopBackOff` |

Every `break-*` target runs `make reset` first, so each fault starts from
`main`'s history. `make reset` undoes any fault.

`lib.sh` holds the shared steps: check out `demo-env` in a temporary git
worktree (your checkout is untouched), edit the manifest, commit, and push.
The fault scripts deliberately share no code with the agent, so the evals test
the agent against something it didn't write.

Planned: an "escalate" scenario where the latest commit is innocent and the
correct outcome is to escalate rather than revert, and configuration drift
(`OutOfSync`), which calls for an ArgoCD sync rather than a revert.
