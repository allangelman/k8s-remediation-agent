#!/usr/bin/env bash
# Bring the cluster fully in line with demo-env before a fault, so nothing
# ArgoCD does on its own afterwards can undo the fault. Two cases need a sync:
#
#   1. The app is OutOfSync: live changes made outside Git (the drift faults).
#      With self-heal off, auto-sync only deploys new commits, so the app would
#      stay OutOfSync after `make reset`.
#   2. The app is Synced, but ArgoCD has never run a sync at the current
#      revision. This happens when main gets a commit that doesn't touch
#      apps/: ArgoCD compares the new commit, sees the cluster already matches,
#      and reports Synced without syncing. Auto-sync still owes that revision
#      one sync, and it runs the first time the app goes OutOfSync, which
#      erases a drift fault seconds after it is injected.
#
# Usage: CONTEXT=kind-remediation scripts/sync-drift.sh
set -euo pipefail
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n argocd)

app() { "${kubectl[@]}" get application demo-app -o jsonpath="$1"; }

# Wait for the refresh to finish (ArgoCD removes the annotation) and for any
# auto-sync of a new commit to complete.
for ((i = 0; i < 30; i++)); do
  refresh=$(app '{.metadata.annotations.argocd\.argoproj\.io/refresh}')
  phase=$(app '{.status.operationState.phase}')
  [[ -z $refresh && $phase != Running ]] && break
  sleep 2
done

sync=$(app '{.status.sync.status}')
revision=$(app '{.status.sync.revision}')
last_synced=$(app '{.status.operationState.syncResult.revision}')

if [[ $sync == OutOfSync ]]; then
  echo "demo-app is OutOfSync with Git; syncing to undo live changes..."
elif [[ $last_synced != "$revision" ]]; then
  echo "demo-app has not been synced at ${revision:0:7} yet; syncing so auto-sync can't undo the fault later..."
else
  exit 0
fi
"${kubectl[@]}" patch application demo-app --type merge -p '{"operation":{"sync":{}}}' >/dev/null
