# faults

Scripts that break the sample app in specific, repeatable ways. There are two
kinds.

## Bad commits

Each script pushes a realistic bad commit to the `demo-env` branch (the one
ArgoCD deploys from), so there's real deploy history for the agent to
investigate. The old pods keep serving in every case, but the rollout stalls
and ArgoCD reports the app **Synced but Degraded**. The fix is to revert the
commit.

| Make target   | Script             | Commit                                | Symptom |
|---------------|--------------------|---------------------------------------|---------|
| `break-image` | `bad-image-tag.sh` | "Pin podinfo image to v6.15.0"        | `ImagePullBackOff`: the tag doesn't exist |
| `break-crash` | `crash-loop.sh`    | "Set podinfo listen port explicitly"  | `CrashLoopBackOff`: `--port=98989` is an invalid port |
| `break-oom`   | `oom.sh`           | "Right-size podinfo memory to 8Mi"    | `OOMKilled` (exit 137) on startup, then `CrashLoopBackOff` |

`lib.sh` holds the shared steps: check out `demo-env` in a temporary git
worktree (your checkout is untouched), edit the manifest, commit, and push.
None of the fault scripts share code with the agent, so the evals test the
agent against something it didn't write.

## Drift: live changes outside Git

These scripts change the live Deployment with kubectl and leave Git alone, so
ArgoCD reports the app **OutOfSync**. The latest commit is innocent, so
reverting it would be wrong. Each one leaves a different trail in
`managedFields`, which is how the agent tells them apart.

| Make target    | Script                | Live change | Field manager | Correct outcome |
|----------------|-----------------------|-------------|---------------|-----------------|
| `break-drift`  | `drift-automation.sh` | memory request and limit to 8Mi: `OOMKilled`, Degraded | `legacy-tuner` (automation) | `sync_app` back to Git |
| `break-manual` | `manual-bad-image.sh` | image tag to `v6.15.0`: `ImagePullBackOff`, Degraded | `kubectl-set` (a person, just now) | escalate; `sync_app` refuses too |
| `break-hotfix` | `manual-hotfix.sh`    | memory limit 128Mi to 256Mi; stays Healthy | `kubectl-edit` (a person, just now) | escalate: ask a human to commit it to Git or drop it |

## Reset

Every `break-*` target runs `make reset` first, so each fault starts from
`main`'s history. `make reset` undoes any fault. It force-pushes `main` to
`demo-env`, starts an ArgoCD sync if the app is still OutOfSync (self-heal is
off, so auto-sync won't undo live changes; `scripts/sync-drift.sh`), and waits
until ArgoCD is Healthy at `main`'s revision.
