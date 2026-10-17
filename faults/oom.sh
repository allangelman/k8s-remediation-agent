#!/usr/bin/env bash
# Fault 3: memory limit too low -> OOMKilled.
#
# "Right-sizes" podinfo's memory to 8Mi (request and limit), well below what it
# needs to start. The kernel kills it on startup (OOMKilled, exit code 137) and
# Kubernetes restarts it in a loop. The old pods keep serving, but the rollout
# stalls and the app goes Degraded.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

fail_if_broken "memory: 8Mi"
edit_manifest 's|memory: [0-9]+Mi|memory: 8Mi|'
push_fault "Right-size podinfo memory to 8Mi"
