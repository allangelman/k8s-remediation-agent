#!/usr/bin/env bash
# Fault 6: a person's emergency fix -> OutOfSync, still Healthy.
#
# An on-call engineer raises podinfo's memory limit from 128Mi to 256Mi with
# `kubectl edit`, say while chasing a memory leak. The app stays Healthy, but
# it no longer matches Git. Nothing is broken, and syncing would undo the
# person's fix, so the correct outcome is to take no action and ask a human to
# commit the change to Git (or drop it).
set -euo pipefail
kubectl=(kubectl --context "${CONTEXT:?CONTEXT must be set}" -n demo)

# kubectl edit opens $KUBE_EDITOR on the live object; sed makes the edit a
# person would make by hand. The field manager is still kubectl-edit.
KUBE_EDITOR="sed -i.bak -E s/memory:\ 128Mi/memory:\ 256Mi/" "${kubectl[@]}" edit deploy/demo-app
