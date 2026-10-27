#!/usr/bin/env bash
# Undo live changes made outside Git (the drift faults). With self-heal off,
# ArgoCD auto-sync only deploys new commits, so the app would stay OutOfSync
# after `make reset`. Once ArgoCD has caught up with Git, start a sync if the
# app is still OutOfSync.
#
# Usage: CONTEXT=kind-remediation scripts/sync-drift.sh
set -euo pipefail
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n argocd)

# Wait for the refresh to finish (ArgoCD removes the annotation) and for any
# auto-sync of a new commit to complete.
for ((i = 0; i < 30; i++)); do
  refresh=$("${kubectl[@]}" get application demo-app -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/refresh}')
  phase=$("${kubectl[@]}" get application demo-app -o jsonpath='{.status.operationState.phase}')
  [[ -z $refresh && $phase != Running ]] && break
  sleep 2
done

if [[ $("${kubectl[@]}" get application demo-app -o jsonpath='{.status.sync.status}') == OutOfSync ]]; then
  echo "demo-app is OutOfSync with Git; syncing to undo live changes..."
  "${kubectl[@]}" patch application demo-app --type merge -p '{"operation":{"sync":{}}}' >/dev/null
fi
