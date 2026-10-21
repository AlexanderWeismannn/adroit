package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// behindUpstream builds a main checkout one commit behind its upstream, where
// that commit adds migration.js -- and edits README too, if editReadme -- and
// returns the checkout, the stale commit it sits on and the upstream tip.
func behindUpstream(t *testing.T, editReadme bool) (repo, stale, tip string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	mustRunGit(t, "", "init", "--bare", "-b", "master", origin)

	seed := filepath.Join(root, "seed")
	mustRunGit(t, "", "clone", origin, seed)
	mustRunGit(t, seed, "config", "user.name", "Test User")
	mustRunGit(t, seed, "config", "user.email", "test@example.com")
	mustRunGit(t, seed, "checkout", "-b", "master")
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("readme\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, seed, "add", "README")
	mustRunGit(t, seed, "commit", "-m", "initial")
	mustRunGit(t, seed, "push", "origin", "master")

	repo = filepath.Join(root, "repo")
	mustRunGit(t, "", "clone", origin, repo)
	mustRunGit(t, repo, "config", "user.name", "Test User")
	mustRunGit(t, repo, "config", "user.email", "test@example.com")
	stale = strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(seed, "migration.js"), []byte("upstream\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if editReadme {
		if err := os.WriteFile(filepath.Join(seed, "README"), []byte("readme, revised\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustRunGit(t, seed, "add", "-A")
	mustRunGit(t, seed, "commit", "-m", "add migration")
	mustRunGit(t, seed, "push", "origin", "master")
	tip = strings.TrimSpace(mustRunGit(t, seed, "rev-parse", "HEAD"))
	return repo, stale, tip
}

func setUpSession(t *testing.T, repo string) *GitWorktree {
	t.Helper()
	g := &GitWorktree{
		repoPath:     repo,
		worktreePath: filepath.Join(t.TempDir(), "wt"),
		branchName:   "TASK-1-fresh",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	return g
}

// An untracked draft of a file the upstream adds makes `merge --ff-only`
// refuse, and used to keep the main checkout behind for good. It is stashed,
// not lost, and the checkout fast-forwards.
func TestSetupNewWorktree_StashesUntrackedFilesBlockingFastForward(t *testing.T) {
	repo, _, tip := behindUpstream(t, false)
	if err := os.WriteFile(filepath.Join(repo, "migration.js"), []byte("local draft\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := setUpSession(t, repo)

	if got := strings.TrimSpace(mustRunGit(t, g.worktreePath, "rev-parse", "HEAD")); got != tip {
		t.Fatalf("session HEAD = %s, want upstream tip %s", got, tip)
	}
	if got := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD")); got != tip {
		t.Fatalf("main checkout at %s, want it fast-forwarded to %s", got, tip)
	}
	if got := strings.TrimSpace(mustRunGit(t, repo, "stash", "list")); !strings.Contains(got, baseSyncStashMessage) {
		t.Fatalf("stash list = %q, want an entry labelled %q", got, baseSyncStashMessage)
	}
	draft := mustRunGit(t, repo, "show", "stash@{0}^3:migration.js")
	if draft != "local draft\n" {
		t.Fatalf("stashed draft = %q, want the local draft", draft)
	}
}

// A tracked edit still refuses the fast-forward. The draft stashed for the
// retry must come straight back rather than be stranded in the stash.
func TestSetupNewWorktree_PutsDraftBackWhenTrackedEditBlocksFastForward(t *testing.T) {
	repo, stale, tip := behindUpstream(t, true)
	if err := os.WriteFile(filepath.Join(repo, "migration.js"), []byte("local draft\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("work in progress\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := setUpSession(t, repo)

	if got := strings.TrimSpace(mustRunGit(t, g.worktreePath, "rev-parse", "HEAD")); got != tip {
		t.Fatalf("session HEAD = %s, want upstream tip %s", got, tip)
	}
	if got := strings.TrimSpace(mustRunGit(t, repo, "rev-parse", "HEAD")); got != stale {
		t.Fatalf("main checkout moved to %s, want it left at %s", got, stale)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "migration.js")); string(b) != "local draft\n" {
		t.Fatalf("draft = %q, want it put back", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "README")); string(b) != "work in progress\n" {
		t.Fatalf("README = %q, want the edit left in place", b)
	}
	if got := strings.TrimSpace(mustRunGit(t, repo, "stash", "list")); got != "" {
		t.Fatalf("stash list = %q, want it empty", got)
	}
}
