#!/usr/bin/env bash
# Fault 1: bad image tag -> ImagePullBackOff.
#
# Changes the podinfo image tag to one that doesn't exist. ArgoCD deploys it,
# the new pod can't pull the image, and the rollout stalls. The old pods keep
# serving, but the app goes Degraded.
#
# The bad tag is the current tag with a "v" prefix (6.15.0 -> v6.15.0), a classic
# typo: Git release tags often have a "v", but podinfo's image tags never do.
# Before pushing, the script checks the registry to confirm the tag really
# doesn't exist, so the fault can't silently turn into a working deploy.
# Images are pulled from GitHub Container Registry (ghcr.io).
set -euo pipefail
source "$(dirname "$0")/lib.sh"

image=ghcr.io/stefanprodan/podinfo
fail_if_broken "image: ${image}:v"

current_tag=$(sed -nE "s|.*image: ${image}:(.*)|\1|p" "$manifest")
bad_tag="v${current_tag}"
if docker manifest inspect "${image}:${bad_tag}" >/dev/null 2>&1; then
  echo "${image}:${bad_tag} exists in the registry, so it wouldn't break anything. Pick another bad tag." >&2
  exit 1
fi

edit_manifest "s|(image: ${image}:).*|\1${bad_tag}|"
push_fault "Pin podinfo image to ${bad_tag}"
