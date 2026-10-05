package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
