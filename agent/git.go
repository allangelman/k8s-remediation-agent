package agent

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := run(ctx, "git", append([]string{"-C", dir}, args...)...)
	return strings.TrimSpace(out), err
}

// Identity used for commits the agent makes, so `git log` shows which changes
// were automated.
const (
	agentName  = "k8s-remediation-agent"
	agentEmail = "k8s-remediation-agent@noreply.local"
)

// How far back in the deploy branch the agent may revert. Older commits are
// out of scope for automated remediation.
const maxRevertDepth = 10

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// validatePotentialRevert checks that sha is safe to revert automatically on ref and
// returns its subject line. It only reads the repo.
func validatePotentialRevert(ctx context.Context, repo, ref, sha string) (string, error) {
	if !fullSHA.MatchString(sha) {
		return "", fmt.Errorf("commit_sha must be a full 40-character SHA, got %q", sha)
	}

	recent, err := git(ctx, repo, "rev-list", "-n", fmt.Sprint(maxRevertDepth), ref)
	if err != nil {
		return "", err
	}
	if !slices.Contains(strings.Fields(recent), sha) {
		return "", fmt.Errorf("commit %s is not among the last %d commits on %s", sha, maxRevertDepth, ref)
	}

	parents, err := git(ctx, repo, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return "", err
	}
	if len(strings.Fields(parents)) > 2 {
		return "", fmt.Errorf("commit %s is a merge commit; reverting merges is not automated", sha)
	}

	files, err := git(ctx, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
	if err != nil {
		return "", err
	}
	if files == "" {
		return "", fmt.Errorf("commit %s changes no files", sha)
	}
	for _, f := range strings.Fields(files) {
		if !strings.HasPrefix(f, "apps/") {
			return "", fmt.Errorf("commit %s changes %s, outside apps/; only app manifest changes can be reverted", sha, f)
		}
	}

	// Idempotency: never revert the same commit twice.
	reverted, err := git(ctx, repo, "log", ref, "-n", "1", "--format=%H", "--grep", "This reverts commit "+sha)
	if err != nil {
		return "", err
	}
	if reverted != "" {
		return "", fmt.Errorf("commit %s was already reverted by %s", sha, reverted)
	}

	return git(ctx, repo, "log", "-1", "--format=%s", sha)
}

// revertAndPush reverts sha on the env branch from a temporary worktree (the
// user's checkout is untouched), pushes it, and returns the new commit's SHA.
func revertAndPush(ctx context.Context, cfg Config, sha, subject, reason string) (string, error) {
	dir, err := os.MkdirTemp("", "agent-revert-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := git(ctx, cfg.RepoDir, "worktree", "add", "-q", "--detach", dir, "origin/"+cfg.EnvBranch); err != nil {
		return "", err
	}
	defer git(context.Background(), cfg.RepoDir, "worktree", "remove", "--force", dir)

	if _, err := git(ctx, dir, "revert", "--no-commit", sha); err != nil {
		git(context.Background(), dir, "revert", "--abort")
		return "", fmt.Errorf("revert does not apply cleanly: %w", err)
	}
	if _, err := git(ctx, dir, "-c", "user.name="+agentName, "-c", "user.email="+agentEmail, "commit", "-q",
		"-m", fmt.Sprintf("Revert %q", subject),
		"-m", "This reverts commit "+sha+".",
		"-m", "Reason: "+reason,
		"-m", "Automated remediation by "+agentName+"."); err != nil {
		return "", err
	}
	// A plain push (no force): if the branch moved since we read it, this
	// fails rather than overwriting someone else's commit.
	if _, err := git(ctx, dir, "push", "-q", "origin", "HEAD:refs/heads/"+cfg.EnvBranch); err != nil {
		return "", err
	}
	return git(ctx, dir, "rev-parse", "HEAD")
}
