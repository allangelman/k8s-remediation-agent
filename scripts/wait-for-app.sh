#!/usr/bin/env bash
# Wait until the demo-app ArgoCD Application is Synced and has the given health
# status (e.g. Healthy, Degraded). Shared by `make up`, `make reset`, and the
# fault targets.
#
# Usage: CONTEXT=kind-remediation scripts/wait-for-app.sh <health> [timeout-seconds]
set -euo pipefail

want=$1
timeout=${2:-300}
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n argocd)

echo "Waiting up to ${timeout}s for demo-app to be ${want}..."
for ((elapsed = 0; elapsed < timeout; elapsed += 5)); do
  sync=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.sync.status}')
  health=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.health.status}')
  # Require Synced as well as the health status: it means ArgoCD has deployed
  # the latest commit on demo-env, so the health we see belongs to that commit
  # and not to whatever was running before. Both the healthy state and the
  # faults in this repo are Synced (a bad commit is still in sync with Git).
  if [[ $sync == Synced && $health == "$want" ]]; then
    echo "demo-app is ${health} (sync=${sync})."
    exit 0
  fi
  echo "  sync=${sync:-unknown} health=${health:-unknown}"
  sleep 5
done
echo "Timed out waiting for demo-app to be ${want}. Run 'make status' to investigate." >&2
exit 1
