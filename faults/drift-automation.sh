#!/usr/bin/env bash
# Fault 4: drift from automation -> OOMKilled, OutOfSync.
#
# A "legacy tuning script" right-sizes podinfo's memory to 8Mi directly on the
# live Deployment, bypassing Git. It writes with its own field manager
# (legacy-tuner), the way a script or controller would, so managedFields shows
# automation, not a person. New pods are OOMKilled; ArgoCD reports the app
# OutOfSync and Degraded. Git is still correct, so the fix is an ArgoCD sync.
set -euo pipefail
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n demo)

"${kubectl[@]}" set resources deploy/demo-app -c podinfo \
  --limits=memory=8Mi --requests=memory=8Mi --field-manager=legacy-tuner
