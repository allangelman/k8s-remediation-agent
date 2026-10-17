#!/usr/bin/env bash
# Fault 2: bad config -> CrashLoopBackOff.
#
# Sets podinfo's listen port explicitly, with a typo: 98989 instead of 9898.
# That isn't a valid port, so podinfo exits on startup ("listen tcp: address
# 98989: invalid port") and Kubernetes restarts it in a loop. The old pods keep
# serving, but the rollout stalls and the app goes Degraded.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

fail_if_broken "--port=98989"
# Add a command line right after the image line. (It has to be the full
# command: podinfo's image has no ENTRYPOINT, so args alone would replace the
# binary itself and fail differently.)
edit_manifest 's|^( *)(image: ghcr.io/stefanprodan/podinfo:.*)$|\1\2\
\1command: ["./podinfo", "--port=98989"]|'
push_fault "Set podinfo listen port explicitly"
