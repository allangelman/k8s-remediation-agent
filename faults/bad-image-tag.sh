#!/usr/bin/env bash
# Fault 1: bad image tag.
#
# Pushes a commit to the environment branch that changes the podinfo image tag
# to one that doesn't exist. ArgoCD deploys it, the new pods fail with
# ImagePullBackOff, and the rollout stalls. The old pods keep serving, but the
# app goes Degraded.
#
# The bad tag is the current tag with a "v" prefix (6.15.0 -> v6.15.0), a classic
# typo: Git release tags often have a "v", but podinfo's image tags never do.
# Before pushing, the script checks the registry to confirm the tag really
# doesn't exist, so the fault can't silently turn into a working deploy.
#
# Images are pulled from GitHub Container Registry (ghcr.io), where podinfo's
# author publishes them.
#
# The commit is made in a temporary git worktree, so your checkout is untouched.
set -euo pipefail

env_branch=${ENV_BRANCH:-demo-env}
image=ghcr.io/stefanprodan/podinfo
manifest=apps/demo-app/deployment.yaml

repo=$(git rev-parse --show-toplevel)
worktree=$(mktemp -d)
trap 'git -C "$repo" worktree remove --force "$worktree" 2>/dev/null || rm -rf "$worktree"' EXIT

git -C "$repo" fetch -q origin "$env_branch"
git -C "$repo" worktree add -q --detach "$worktree" "origin/$env_branch"

current_tag=$(sed -nE "s|.*image: ${image}:(.*)|\1|p" "$worktree/$manifest")
if [[ $current_tag == v* ]]; then
  echo "${env_branch} already has a bad tag (${current_tag}). Run 'make reset' first." >&2
  exit 1
fi
bad_tag="v${current_tag}"
if docker manifest inspect "${image}:${bad_tag}" >/dev/null 2>&1; then
  echo "${image}:${bad_tag} exists in the registry, so it wouldn't break anything. Pick another bad tag." >&2
  exit 1
fi

sed -i.bak -E "s|(image: ${image}:).*|\1${bad_tag}|" "$worktree/$manifest"
rm "$worktree/$manifest.bak"

git -C "$worktree" commit -q -am "Pin podinfo image to ${bad_tag}"
git -C "$worktree" push -q origin "HEAD:refs/heads/$env_branch"
echo "Pushed bad image tag ${bad_tag} to ${env_branch} ($(git -C "$worktree" rev-parse --short HEAD))."
