# Shared setup for fault scripts that push a bad commit to the environment
# branch (demo-env).
#
# Sourcing this file checks out the latest demo-env into a temporary git
# worktree (a separate directory), so the fault is committed there and your own
# checkout and uncommitted work are never touched. The worktree is removed when
# the script exits. A fault script then:
#
#   fail_if_broken "<pattern>"     # refuse if this fault is already applied
#   edit_manifest "<sed expr>"     # edit $manifest (the worktree's deployment.yaml)
#   push_fault "<commit message>"  # commit and push to demo-env

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
