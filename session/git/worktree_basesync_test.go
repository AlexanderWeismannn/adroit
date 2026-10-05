package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An untracked file in the main checkout that the upstream also adds makes
// `merge --ff-only` refuse. The session must still be cut from the upstream tip,
// not from the stale local branch -- this is what pinned every new session to a
// weeks-old master.
func TestSetupNewWorktree_BranchesFromUpstreamWhenFastForwardIsBlocked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	mustRunGit(t, "", "init", "--bare", "-b", "master", origin)

	seed := filepath.Join(root, "seed")
	mustRunGit(t, "", "clone", origin, seed)
	mustRunGit(t, seed, "config", "user.name", "Test User")
	mustRunGit(t, seed, "config", "user.email", "test@example.com")
	mustRunGit(t, seed, "checkout", "-b", "master")
	mustRunGit(t, seed, "commit", "--allow-empty", "-m", "initial")
	mustRunGit(t, seed, "push", "origin", "master")

	repo := filepath.Join(root, "repo")
	mustRunGit(t, "", "clone", origin, repo)
	stale := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(seed, "migration.js"), []byte("upstream\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, seed, "add", "migration.js")
	mustRunGit(t, seed, "commit", "-m", "add migration")
	mustRunGit(t, seed, "push", "origin", "master")
	tip := strings.TrimSpace(mustRunGit(t, seed, "rev-parse", "HEAD"))

	// The stray local draft that blocks the fast-forward.
	if err := os.WriteFile(filepath.Join(repo, "migration.js"), []byte("local draft\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := &GitWorktree{
		repoPath:     repo,
		worktreePath: filepath.Join(root, "wt"),
		branchName:   "TASK-1-fresh",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if got := strings.TrimSpace(mustRunGit(t, g.worktreePath, "rev-parse", "HEAD")); got != tip {
		t.Fatalf("session HEAD = %s, want upstream tip %s", got, tip)
	}
	if g.baseCommitSHA != tip {
		t.Fatalf("baseCommitSHA = %s, want %s", g.baseCommitSHA, tip)
	}
	// The main checkout and its draft are left alone.
	if got := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD")); got != stale {
		t.Fatalf("main checkout moved to %s, want it left at %s", got, stale)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "migration.js")); string(b) != "local draft\n" {
		t.Fatalf("local draft was touched: %q", b)
	}
}
