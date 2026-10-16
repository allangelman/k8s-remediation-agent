#!/usr/bin/env bash
# Wait until the demo-app ArgoCD Application reaches the given health status
# (e.g. Healthy, Degraded). For Healthy, also require it to be Synced.
#
# Usage: CONTEXT=kind-remediation infra/wait-for-app.sh <health> [timeout-seconds]
set -euo pipefail

want=$1
timeout=${2:-300}
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n argocd)

echo "Waiting up to ${timeout}s for demo-app to be ${want}..."
for ((elapsed = 0; elapsed < timeout; elapsed += 5)); do
  sync=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.sync.status}')
  health=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.health.status}')
  if [[ $health == "$want" && ($want != Healthy || $sync == Synced) ]]; then
    echo "demo-app is ${health} (sync=${sync})."
    exit 0
  fi
  echo "  sync=${sync:-unknown} health=${health:-unknown}"
  sleep 5
done
echo "Timed out waiting for demo-app to be ${want}. Run 'make status' to investigate." >&2
exit 1
