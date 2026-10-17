package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRepo creates a throwaway git repo and returns a function that commits a
// file change and returns the new commit's SHA.
func testRepo(t *testing.T) (dir string, commit func(path, content, msg string) string) {
	t.Helper()
	dir = t.TempDir()
	gitT := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitT("init", "-q", "-b", "main")
	commit = func(path, content, msg string) string {
		full := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT("add", "-A")
		gitT("commit", "-q", "-m", msg)
		return gitT("rev-parse", "HEAD")
	}
	return dir, commit
}

func TestValidatePotentialRevert(t *testing.T) {
	ctx := context.Background()
	dir, commit := testRepo(t)

	old := commit("apps/demo/deployment.yaml", "image: v1\n", "initial")
	// Push old out of the revertable window.
	for i := 0; i < maxRevertDepth; i++ {
		commit("apps/demo/deployment.yaml", strings.Repeat("x", i+1)+"\n", "filler")
	}
	outside := commit("Makefile", "all:\n", "touch a file outside apps/")
	bad := commit("apps/demo/deployment.yaml", "image: v2-typo\n", "Pin image to v2-typo")

	if subject, err := validatePotentialRevert(ctx, dir, "main", bad); err != nil || subject != "Pin image to v2-typo" {
		t.Errorf("valid revert: got %q, %v", subject, err)
	}

	rejects := map[string]string{
		"short sha":         bad[:7],
		"not a sha":         "--all; rm -rf /",
		"too old":           old,
		"outside apps/":     outside,
		"unknown commit id": strings.Repeat("a", 40),
	}
	for name, sha := range rejects {
		if _, err := validatePotentialRevert(ctx, dir, "main", sha); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}

	// Once reverted, the same commit must not be reverted again.
	commit("apps/demo/deployment.yaml", "image: v1\n", "Revert\n\nThis reverts commit "+bad+".")
	if _, err := validatePotentialRevert(ctx, dir, "main", bad); err == nil || !strings.Contains(err.Error(), "already reverted") {
		t.Errorf("already reverted: got %v", err)
	}
}
