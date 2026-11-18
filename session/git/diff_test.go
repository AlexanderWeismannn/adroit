package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAdded   int
		wantRemoved int
	}{
		{
			name:        "empty output",
			input:       "",
			wantAdded:   0,
			wantRemoved: 0,
		},
		{
			name:        "single file",
			input:       "3\t1\tfoo.go\n",
			wantAdded:   3,
			wantRemoved: 1,
		},
		{
			name:        "multiple files sum correctly",
			input:       "3\t1\tfoo.go\n10\t2\tbar/baz.go\n",
			wantAdded:   13,
			wantRemoved: 3,
		},
		{
			name:        "binary files are skipped",
			input:       "5\t0\tfoo.go\n-\t-\timage.png\n2\t2\tbar.go\n",
			wantAdded:   7,
			wantRemoved: 2,
		},
		{
			name:        "path with tabs is preserved via SplitN",
			input:       "4\t4\tpath\twith\ttabs.go\n",
			wantAdded:   4,
			wantRemoved: 4,
		},
		{
			name:        "trailing newlines do not add garbage",
			input:       "1\t0\ta.go\n\n\n",
			wantAdded:   1,
			wantRemoved: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAdded, gotRemoved := parseNumstat(tt.input)
			if gotAdded != tt.wantAdded || gotRemoved != tt.wantRemoved {
				t.Errorf("parseNumstat(%q) = (%d, %d), want (%d, %d)",
					tt.input, gotAdded, gotRemoved, tt.wantAdded, tt.wantRemoved)
			}
		})
	}
}

// The regression this guards: the counts on the list row are the session's own
// work, not the base branch's. A repository whose local master has fallen behind
// its remote hands the session a base commit that predates 47 merged PRs, and
// every one of their lines is then attributed to the session -- measured at
// +80350/-13203 for a branch carrying 7 commits.
func TestDiffNumstat_ExcludesBaseBranchCommitsTheSessionOnlyInherited(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", "-b", "master", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	commit := func(name string, lines int) {
		body := strings.Repeat("x\n", lines)
		if err := os.WriteFile(filepath.Join(repoPath, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		mustRunGit(t, repoPath, "add", name)
		mustRunGit(t, repoPath, "commit", "-m", name)
	}

	commit("initial.txt", 1)
	stale := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))

	// Master moves on by 500 lines the session had no hand in, and the session's
	// branch forks from there.
	commit("upstream.txt", 500)
	mustRunGit(t, repoPath, "update-ref", "refs/remotes/origin/master", "HEAD")
	mustRunGit(t, repoPath, "branch", "feature/test")

	// The local checkout is left where it was, which is what a stale local master
	// looks like and what the session records as its base.
	mustRunGit(t, repoPath, "reset", "--hard", stale)

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".adroit", "worktrees", "feature-test"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	// Setup records the branch tip for an existing branch; the inflated case is
	// the stale local base, so put the session there.
	g.baseCommitSHA = stale
	g.baseBranch = "master"

	// The session's own work: three lines on a file of its own.
	own := filepath.Join(g.worktreePath, "mine.txt")
	if err := os.WriteFile(own, []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatalf("write mine.txt: %v", err)
	}

	stats := g.DiffNumstat(true)
	if stats.Error != nil {
		t.Fatalf("DiffNumstat() error = %v", stats.Error)
	}
	if stats.Added != 3 || stats.Removed != 0 {
		t.Fatalf("DiffNumstat() = +%d/-%d, want +3/-0 (the 500 inherited lines must not be counted)", stats.Added, stats.Removed)
	}
}

// The resolved base only ever moves forward from the recorded one, so a session
// forked from somewhere other than the default branch is never measured from
// further back than it was before.
func TestResolveDiffBase_NeverMovesEarlierThanTheRecordedBase(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", "-b", "master", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(repoPath, "a.txt"), []byte("a\n"), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	mustRunGit(t, repoPath, "add", "a.txt")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "update-ref", "refs/remotes/origin/master", "HEAD")

	// A stacked branch: forked from another feature branch that is itself ahead
	// of master.
	mustRunGit(t, repoPath, "checkout", "-b", "feature/parent")
	if err := os.WriteFile(filepath.Join(repoPath, "b.txt"), []byte("b\n"), 0644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	mustRunGit(t, repoPath, "add", "b.txt")
	mustRunGit(t, repoPath, "commit", "-m", "parent work")
	parent := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))
	mustRunGit(t, repoPath, "branch", "feature/child")

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".adroit", "worktrees", "feature-child"),
		branchName:       "feature/child",
		isExistingBranch: true,
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	got, err := g.resolveDiffBase()
	if err != nil {
		t.Fatalf("resolveDiffBase() error = %v", err)
	}
	if got != parent {
		t.Fatalf("resolveDiffBase() = %q, want the recorded parent-branch base %q", got, parent)
	}
}

// newSessionWorktree is a repository with one commit on master, mirrored to
// origin/master, and a session worktree on feature/test forked from it.
func newSessionWorktree(t *testing.T) (*GitWorktree, string) {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", "-b", "master", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoPath, "a.txt"), []byte("a\n"), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	mustRunGit(t, repoPath, "add", "a.txt")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "update-ref", "refs/remotes/origin/master", "HEAD")
	mustRunGit(t, repoPath, "branch", "feature/test")

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".adroit", "worktrees", "feature-test"),
		branchName:       "feature/test",
		baseBranch:       "master",
		isExistingBranch: true,
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	g.baseCommitSHA = strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))
	return g, repoPath
}

// Staging copies the whole real index -- 2.6MB on a large repository -- and
// rewrites it with the add. Done for every session every fifteen seconds and for
// the selected one on every diff, whether anything had changed or not, it was
// megabytes a second of disk writes. A staging that would come out the same is
// skipped; a new untracked file is still staged at once.
func TestStagingUntrackedIsSkippedUntilSomethingChanged(t *testing.T) {
	g, _ := newSessionWorktree(t)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(g.worktreePath, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("mine.txt", "a\nb\nc\n")

	stats := g.DiffNumstat(true)
	if stats.Error != nil || stats.Added != 3 {
		t.Fatalf("DiffNumstat() = +%d (%v), want +3", stats.Added, stats.Error)
	}
	private, err := g.privateIndexPath()
	if err != nil {
		t.Fatalf("privateIndexPath() error = %v", err)
	}
	// Back-dated, so a rewrite shows as a fresh modification time.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(private, old, old); err != nil {
		t.Fatalf("back-date private index: %v", err)
	}

	if stats := g.Diff(); stats.Error != nil || stats.Added != 3 {
		t.Fatalf("Diff() = +%d (%v), want +3", stats.Added, stats.Error)
	}
	after, err := os.Stat(private)
	if err != nil {
		t.Fatalf("stat private index: %v", err)
	}
	if !after.ModTime().Equal(old) {
		t.Fatalf("the private index was rewritten with nothing changed")
	}

	write("other.txt", "d\ne\n")
	if stats := g.DiffNumstat(true); stats.Error != nil || stats.Added != 5 {
		t.Fatalf("DiffNumstat() after a new untracked file = +%d (%v), want +5", stats.Added, stats.Error)
	}
}

// The resolved base is kept only while what it was worked out from stands. It
// used to be kept for 30 seconds whatever happened, so a fetch that moved the
// base branch left the counts measured from the old fork point until it ran out.
func TestResolveDiffBaseFollowsTheBaseBranchAtOnce(t *testing.T) {
	g, repoPath := newSessionWorktree(t)
	first, err := g.resolveDiffBase()
	if err != nil {
		t.Fatalf("resolveDiffBase() error = %v", err)
	}

	// The session merges in a newer origin/master: the fork point moves forward.
	if err := os.WriteFile(filepath.Join(repoPath, "b.txt"), []byte("b\n"), 0644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	mustRunGit(t, repoPath, "add", "b.txt")
	mustRunGit(t, repoPath, "commit", "-m", "upstream")
	mustRunGit(t, repoPath, "update-ref", "refs/remotes/origin/master", "HEAD")
	mustRunGit(t, g.worktreePath, "merge", "--ff-only", "origin/master")
	want := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))

	got, err := g.resolveDiffBase()
	if err != nil {
		t.Fatalf("resolveDiffBase() error = %v", err)
	}
	if got == first || got != want {
		t.Fatalf("resolveDiffBase() = %q, want the new fork point %q", got, want)
	}
}

// ChangedSinceDiff is what lets a still session back its diff off: it must see
// a commit made with nothing on screen, or the counts sit stale for a minute.
func TestChangedSinceDiffSeesACommit(t *testing.T) {
	g, _ := newSessionWorktree(t)
	if stats := g.DiffNumstat(false); stats.Error != nil {
		t.Fatalf("DiffNumstat() error = %v", stats.Error)
	}
	if g.ChangedSinceDiff() {
		t.Fatalf("ChangedSinceDiff() = true with nothing done")
	}
	if err := os.WriteFile(filepath.Join(g.worktreePath, "c.txt"), []byte("c\n"), 0644); err != nil {
		t.Fatalf("write c.txt: %v", err)
	}
	mustRunGit(t, g.worktreePath, "add", "c.txt")
	mustRunGit(t, g.worktreePath, "commit", "-m", "work")
	if !g.ChangedSinceDiff() {
		t.Fatalf("ChangedSinceDiff() = false after a commit")
	}
}
