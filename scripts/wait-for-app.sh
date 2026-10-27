#!/usr/bin/env bash
# Wait until the demo-app ArgoCD Application has the given health status (e.g.
# Healthy, Degraded) and sync status (Synced unless SYNC is set; the drift
# faults wait for OutOfSync). If REVISION is set, also wait until ArgoCD
# compares against that Git revision. Shared by `make up`, `make reset`, and
# the fault targets.
#
# Usage: CONTEXT=kind-remediation [SYNC=OutOfSync] [REVISION=<sha>] scripts/wait-for-app.sh <health> [timeout-seconds]
set -euo pipefail

want=$1
want_sync=${SYNC:-Synced}
want_revision=${REVISION:-}
timeout=${2:-300}
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n argocd)

echo "Waiting up to ${timeout}s for demo-app to be ${want_sync} and ${want}..."
for ((elapsed = 0; elapsed < timeout; elapsed += 5)); do
  sync=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.sync.status}')
  health=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.health.status}')
  revision=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.sync.revision}')
  # Require the sync status as well as the health status. Synced means ArgoCD
  # has deployed the latest commit on demo-env, so the health we see belongs to
  # that commit and not to whatever was running before. The healthy state and
  # the bad-commit faults are Synced (a bad commit is still in sync with Git);
  # the drift faults are OutOfSync.
  if [[ $sync == "$want_sync" && $health == "$want" && ( -z $want_revision || $revision == "$want_revision" ) ]]; then
    echo "demo-app is ${health} (sync=${sync})."
    exit 0
  fi
  echo "  sync=${sync:-unknown} health=${health:-unknown}"
  sleep 5
done
echo "Timed out waiting for demo-app to be ${want_sync} and ${want}. Run 'make status' to investigate." >&2
exit 1
