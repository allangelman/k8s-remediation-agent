# Shared setup for fault scripts that push a bad commit to the environment
# branch. Source it, edit "$manifest", then call push_fault "<message>".
#
# The commit is made in a temporary git worktree, so your checkout is untouched.

env_branch=${ENV_BRANCH:-demo-env}
repo=$(git rev-parse --show-toplevel)
worktree=$(mktemp -d)
manifest="$worktree/apps/demo-app/deployment.yaml"
trap 'git -C "$repo" worktree remove --force "$worktree" 2>/dev/null || rm -rf "$worktree"' EXIT

git -C "$repo" fetch -q origin "$env_branch"
git -C "$repo" worktree add -q --detach "$worktree" "origin/$env_branch"

# fail_if_broken <grep pattern> exits if the manifest already contains the
# fault, so a fault can't be stacked on itself without a reset.
fail_if_broken() {
  if grep -qE "$1" "$manifest"; then
    echo "${env_branch} already has this fault. Run 'make reset' first." >&2
    exit 1
  fi
}

# edit_manifest <sed expression> edits the manifest in place (portable
# across GNU and BSD sed).
edit_manifest() {
  sed -i.bak -E "$1" "$manifest"
  rm "$manifest.bak"
}

push_fault() {
  git -C "$worktree" commit -q -am "$1"
  git -C "$worktree" push -q origin "HEAD:refs/heads/$env_branch"
  echo "Pushed \"$1\" to ${env_branch} ($(git -C "$worktree" rev-parse --short HEAD))."
}
