#!/usr/bin/env bash
# Fault 1: bad image tag.
#
# Pushes a commit to the environment branch that bumps podinfo to a tag that
# doesn't exist. ArgoCD deploys it, the new pods fail with ImagePullBackOff, and
# the rollout stalls. The old pods keep serving, but the app goes Degraded.
#
# The commit is made in a temporary git worktree, so your checkout is untouched.
set -euo pipefail

env_branch=${ENV_BRANCH:-demo-env}
bad_tag=6.15.9 # does not exist; the real image is 6.15.0
manifest=apps/demo-app/deployment.yaml

repo=$(git rev-parse --show-toplevel)
worktree=$(mktemp -d)
trap 'git -C "$repo" worktree remove --force "$worktree" 2>/dev/null || rm -rf "$worktree"' EXIT

git -C "$repo" fetch -q origin "$env_branch"
git -C "$repo" worktree add -q --detach "$worktree" "origin/$env_branch"

sed -i.bak -E "s|(image: ghcr.io/stefanprodan/podinfo:).*|\1${bad_tag}|" "$worktree/$manifest"
rm "$worktree/$manifest.bak"

git -C "$worktree" commit -q -am "Bump podinfo to ${bad_tag}"
git -C "$worktree" push -q origin "HEAD:refs/heads/$env_branch"
echo "Pushed bad image tag ${bad_tag} to ${env_branch} ($(git -C "$worktree" rev-parse --short HEAD))."
