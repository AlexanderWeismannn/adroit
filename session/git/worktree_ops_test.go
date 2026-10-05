package git

import (
	"encoding/json"
	"github.com/AlexanderWeismannn/adroit/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

func TestSetupFromExistingBranch_RemovesOrphanedDirectory(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	worktreePath := filepath.Join(tempHome, ".adroit", "worktrees", "feature-test")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir orphaned worktree: %v", err)
	}

	junkPath := filepath.Join(worktreePath, "orphan.txt")
	if err := os.WriteFile(junkPath, []byte("orphaned\n"), 0644); err != nil {
		t.Fatalf("write orphan marker: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if _, err := os.Stat(junkPath); !os.IsNotExist(err) {
		t.Fatalf("orphan marker still exists after Setup, err = %v", err)
	}

	if valid, err := g.IsValidWorktree(); err != nil {
		t.Fatalf("IsValidWorktree() error = %v", err)
	} else if !valid {
		t.Fatal("expected Setup() to recreate a valid worktree")
	}

	currentBranch := mustRunGit(t, worktreePath, "branch", "--show-current")
	if currentBranch != "feature/test\n" {
		t.Fatalf("current branch = %q, want %q", currentBranch, "feature/test\n")
	}
}

func TestSetupFromExistingBranch_RecordsBaseCommit(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".adroit", "worktrees", "feature-test"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	want := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "feature/test"))
	if got := g.GetBaseCommitSHA(); got != want {
		t.Fatalf("GetBaseCommitSHA() = %q, want %q", got, want)
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}

	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

// A worktree that outlives the state entry naming it — the config directory moved,
// the state file was reset — still holds the branch, and git refuses to add a second
// worktree for it. Setup must adopt the existing one rather than fail forever.
func TestSetupFromExistingBranch_AdoptsWorktreeHoldingBranchElsewhere(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	// The worktree the old config directory left behind, with uncommitted work in it.
	strandedPath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test")
	mustRunGit(t, repoPath, "worktree", "add", strandedPath, "feature/test")
	workPath := filepath.Join(strandedPath, "in-progress.txt")
	if err := os.WriteFile(workPath, []byte("uncommitted\n"), 0644); err != nil {
		t.Fatalf("write in-progress work: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(tempHome, ".adroit", "worktrees", "feature-test"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	// git reports the real path, so compare resolved: on macOS the temp
	// directory is /var/..., which is a symlink to /private/var/....
	if got := g.GetWorktreePath(); !samePath(got, strandedPath) {
		t.Fatalf("GetWorktreePath() = %q, want the stranded worktree %q", got, strandedPath)
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Fatalf("uncommitted work was destroyed: %v", err)
	}
	want := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "feature/test"))
	if got := g.GetBaseCommitSHA(); got != want {
		t.Fatalf("GetBaseCommitSHA() = %q, want %q", got, want)
	}

	// And if the session then fails to start, giving up must leave the adopted
	// worktree as it was found: removing it would destroy the work above.
	if err := g.AbandonSetup(); err != nil {
		t.Fatalf("AbandonSetup() error = %v", err)
	}
	if _, err := os.Stat(workPath); err != nil {
		t.Fatalf("a failed start destroyed the adopted worktree's work: %v", err)
	}
}

// A registration left over for a directory that is gone must not block the add.
func TestSetupFromExistingBranch_PrunesStaleRegistrationForMissingDirectory(t *testing.T) {
	tempHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		_ = os.Setenv("HOME", originalHome)
	}()

	repoPath := filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")

	strandedPath := filepath.Join(tempHome, ".claude-squad", "worktrees", "feature-test")
	mustRunGit(t, repoPath, "worktree", "add", strandedPath, "feature/test")
	if err := os.RemoveAll(strandedPath); err != nil {
		t.Fatalf("remove stranded worktree directory: %v", err)
	}

	newPath := filepath.Join(tempHome, ".adroit", "worktrees", "feature-test")
	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     newPath,
		branchName:       "feature/test",
		isExistingBranch: true,
	}

	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if got := g.GetWorktreePath(); got != newPath {
		t.Fatalf("GetWorktreePath() = %q, want %q", got, newPath)
	}
	if valid, err := g.IsValidWorktree(); err != nil || !valid {
		t.Fatalf("IsValidWorktree() = %v, %v; want true, nil", valid, err)
	}
}

// Naming a session after the ticket is the obvious thing to do, and with a
// prefix configured that used to produce "TASK-TASK-5635-UI" -- a branch nobody
// meant, which then names the worktree directory and every push.
func TestApplyBranchPrefixDoesNotDoubleUp(t *testing.T) {
	cases := []struct {
		prefix, name, want string
	}{
		{"TASK-", "5766-Zebra-Dynamics", "TASK-5766-Zebra-Dynamics"},
		{"TASK-", "TASK-5635-UI", "TASK-5635-UI"},
		// A prefix typed either way means the same thing; the name keeps its own case.
		{"TASK-", "task-5635-ui", "task-5635-ui"},
		{"task-", "TASK-5635-UI", "TASK-5635-UI"},
		{"", "5635-UI", "5635-UI"},
		// Only a leading match counts: this is a name that merely mentions it.
		{"TASK-", "revert-TASK-5635", "TASK-revert-TASK-5635"},
	}
	for _, c := range cases {
		if got := applyBranchPrefix(c.prefix, c.name); got != c.want {
			t.Errorf("applyBranchPrefix(%q, %q) = %q, want %q", c.prefix, c.name, got, c.want)
		}
	}
}

// syncRepo builds a repository with an origin whose default branch has moved on
// since the checkout last pulled: origin/master carries a second commit, the
// local master does not, and the local branch tracks it. Returns the checkout
// path and the SHA origin/master points at.
func syncRepo(t *testing.T) (repoPath, remoteHead string) {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	upstream := filepath.Join(root, "upstream")
	repoPath = filepath.Join(root, "repo")

	mustRunGit(t, "", "init", "--bare", "--initial-branch=master", origin)

	// A second working clone is what advances origin; pushing from the checkout
	// under test would leave its own master already up to date.
	mustRunGit(t, "", "clone", origin, upstream)
	mustRunGit(t, upstream, "config", "user.name", "Test User")
	mustRunGit(t, upstream, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(upstream, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, upstream, "add", "README.md")
	mustRunGit(t, upstream, "commit", "-m", "initial")
	mustRunGit(t, upstream, "push", "origin", "master")

	mustRunGit(t, "", "clone", origin, repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(upstream, "NEWER.md"), []byte("newer\n"), 0644); err != nil {
		t.Fatalf("write NEWER: %v", err)
	}
	mustRunGit(t, upstream, "add", "NEWER.md")
	mustRunGit(t, upstream, "commit", "-m", "advance master")
	mustRunGit(t, upstream, "push", "origin", "master")

	return repoPath, strings.TrimSpace(mustRunGit(t, upstream, "rev-parse", "HEAD"))
}

// withTempHome points HOME at a temp directory so config.LoadConfig and the
// worktree directory do not touch the developer's real ~/.adroit.
func withTempHome(t *testing.T) string {
	t.Helper()

	tempHome := t.TempDir()
	original := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", original) })
	return tempHome
}

// A session cut from a master nobody has pulled in a week starts a week behind,
// and nothing on screen says so -- the cost shows up later as a rebase, a
// conflict, or a CI run against a base that has moved.
func TestSetupNewWorktreeFastForwardsTheDefaultBranchFirst(t *testing.T) {
	tempHome := withTempHome(t)
	repoPath, remoteHead := syncRepo(t)

	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: filepath.Join(tempHome, ".adroit", "worktrees", "feature-sync"),
		branchName:   "feature/sync",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if got := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "master")); got != remoteHead {
		t.Fatalf("master = %s, want it fast-forwarded to origin/master %s", got, remoteHead)
	}
	if g.baseCommitSHA != remoteHead {
		t.Fatalf("baseCommitSHA = %s, want the new branch cut from %s", g.baseCommitSHA, remoteHead)
	}
	if _, err := os.Stat(filepath.Join(g.worktreePath, "NEWER.md")); err != nil {
		t.Fatalf("the worktree should carry the commit master was behind: %v", err)
	}
}

// A checkout parked on a feature branch is parked there on purpose. Nothing is
// fetched or moved, and the session branches from where the user left it.
func TestSetupNewWorktreeLeavesANonDefaultBranchAlone(t *testing.T) {
	tempHome := withTempHome(t)
	repoPath, remoteHead := syncRepo(t)

	mustRunGit(t, repoPath, "checkout", "-b", "parked")
	parkedHead := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))
	if parkedHead == remoteHead {
		t.Fatal("the checkout is meant to start behind origin/master")
	}

	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: filepath.Join(tempHome, ".adroit", "worktrees", "feature-parked"),
		branchName:   "feature/parked",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if g.baseCommitSHA != parkedHead {
		t.Fatalf("baseCommitSHA = %s, want the parked branch's own head %s", g.baseCommitSHA, parkedHead)
	}
}

// Local commits on the default branch outrank the convenience: fast-forwarding
// is impossible and merging or resetting is not a decision to make silently.
func TestSetupNewWorktreeDoesNotTouchADivergedDefaultBranch(t *testing.T) {
	tempHome := withTempHome(t)
	repoPath, remoteHead := syncRepo(t)

	if err := os.WriteFile(filepath.Join(repoPath, "LOCAL.md"), []byte("local\n"), 0644); err != nil {
		t.Fatalf("write LOCAL: %v", err)
	}
	mustRunGit(t, repoPath, "add", "LOCAL.md")
	mustRunGit(t, repoPath, "commit", "-m", "local only")
	divergedHead := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "HEAD"))

	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: filepath.Join(tempHome, ".adroit", "worktrees", "feature-diverged"),
		branchName:   "feature/diverged",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	if got := strings.TrimSpace(mustRunGit(t, repoPath, "rev-parse", "master")); got != divergedHead {
		t.Fatalf("master = %s, want it left at the diverged commit %s", got, divergedHead)
	}
	if g.baseCommitSHA == remoteHead {
		t.Fatal("the session must branch from the local master, not from origin/master")
	}
}

// A ticket convention belongs to the project, so the prefix a session's branch
// gets has to be read for the repository the session is being created in --
// after the root is resolved, since until then there is only some directory
// inside one.
func TestNewGitWorktreeUsesThePerRepoBranchPrefix(t *testing.T) {
	tempHome := withTempHome(t)

	repoPath := filepath.Join(t.TempDir(), "ticketed")
	mustRunGit(t, "", "init", repoPath)

	cfg := map[string]any{
		"branch_prefix": "developer/",
		"repos": map[string]any{
			repoPath: map[string]any{"branch_prefix": "TASK-", "preserve_branch_case": true},
		},
	}
	writeAdroitConfig(t, tempHome, cfg)

	// Created from a subdirectory, to prove the lookup happens against the
	// resolved root rather than against whatever path it was handed.
	sub := filepath.Join(repoPath, "services", "signals")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	_, branch, err := NewGitWorktree(sub, "5635-UI")
	if err != nil {
		t.Fatalf("NewGitWorktree() error = %v", err)
	}
	if branch != "TASK-5635-UI" {
		t.Fatalf("branch = %q, want the repository's own prefix and case preserved", branch)
	}

	// A repository with no entry of its own still gets the global prefix.
	otherPath := filepath.Join(t.TempDir(), "other")
	mustRunGit(t, "", "init", otherPath)
	_, branch, err = NewGitWorktree(otherPath, "Fix-Thing")
	if err != nil {
		t.Fatalf("NewGitWorktree() error = %v", err)
	}
	if branch != "developer/fix-thing" {
		t.Fatalf("branch = %q, want the global prefix and the global case policy", branch)
	}
}

// writeAdroitConfig writes a config into the temporary HOME a test is running
// under, so LoadConfig reads it instead of the developer's own.
func writeAdroitConfig(t *testing.T, home string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".adroit")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// newAdoptionRepo makes a repository with one commit and a branch feature/test,
// under a HOME of its own.
func newAdoptionRepo(t *testing.T) (home, repoPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	repoPath = filepath.Join(t.TempDir(), "repo")
	mustRunGit(t, "", "init", repoPath)
	mustRunGit(t, repoPath, "config", "user.name", "Test User")
	mustRunGit(t, repoPath, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, repoPath, "add", "README.md")
	mustRunGit(t, repoPath, "commit", "-m", "initial")
	mustRunGit(t, repoPath, "branch", "feature/test")
	return home, repoPath
}

// Adopting hands a directory to the session's lifecycle -- pause commits in it,
// kill force-removes it -- so the branch being checked out in the repository
// itself must be refused, never adopted.
func TestSetupFromExistingBranch_RefusesTheRepositorysOwnCheckout(t *testing.T) {
	home, repoPath := newAdoptionRepo(t)
	mustRunGit(t, repoPath, "checkout", "feature/test")

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(home, ".adroit", "worktrees", "feature-test"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}
	err := g.Setup()
	if err == nil || !strings.Contains(err.Error(), "not one Adroit created") {
		t.Fatalf("Setup() error = %v, want a refusal to take over the checkout", err)
	}
	if got := g.GetWorktreePath(); samePath(got, repoPath) {
		t.Fatalf("the session adopted the repository's own checkout")
	}
}

// Two sessions on one worktree means killing either deletes the other's work.
func TestSetupFromExistingBranch_RefusesAWorktreeAnotherSessionOwns(t *testing.T) {
	home, repoPath := newAdoptionRepo(t)
	owned := filepath.Join(home, ".adroit", "worktrees", "feature-test_owned")
	mustRunGit(t, repoPath, "worktree", "add", owned, "feature/test")

	instances, _ := json.Marshal([]map[string]any{{"title": "first", "worktree": map[string]any{"worktree_path": owned}}})
	state, _ := json.Marshal(map[string]any{"instances": json.RawMessage(instances)})
	if err := os.WriteFile(filepath.Join(home, ".adroit", "state.json"), state, 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	g := &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     filepath.Join(home, ".adroit", "worktrees", "feature-test_second"),
		branchName:       "feature/test",
		isExistingBranch: true,
	}
	err := g.Setup()
	if err == nil || !strings.Contains(err.Error(), "another session is using") {
		t.Fatalf("Setup() error = %v, want a refusal to share the worktree", err)
	}
}

// A typed name that lands on a branch that already exists reuses it, and must
// then treat it as pre-existing: Cleanup deletes only branches it created.
func TestSetupOnATypedNameThatAlreadyExistsKeepsTheBranchOnCleanup(t *testing.T) {
	home, repoPath := newAdoptionRepo(t)
	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: filepath.Join(home, ".adroit", "worktrees", "feature-test"),
		branchName:   "feature/test",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := g.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	mustRunGit(t, repoPath, "rev-parse", "--verify", "refs/heads/feature/test")
}

// Counting untracked files must not change the agent's index: an intent-to-add
// entry turns "??" into " A" in its git status, and `git commit -a` then commits
// every stray file in the worktree.
func TestDiffCountsUntrackedFilesWithoutTouchingTheIndex(t *testing.T) {
	home, repoPath := newAdoptionRepo(t)
	g := &GitWorktree{
		repoPath:     repoPath,
		worktreePath: filepath.Join(home, ".adroit", "worktrees", "fresh"),
		branchName:   "fresh",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(g.worktreePath, "new.txt"), []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stats := g.DiffNumstat(true)
	if stats.Error != nil || stats.Added != 3 {
		t.Fatalf("DiffNumstat(true) = %+v, want 3 added", stats)
	}
	// A later pass that does not re-stage still sees it, off the private index.
	if again := g.DiffNumstat(false); again.Added != 3 {
		t.Fatalf("DiffNumstat(false) = %+v, want the untracked file still counted", again)
	}
	if full := g.Diff(); full.Added != 3 || !strings.Contains(full.Content, "new.txt") {
		t.Fatalf("Diff() = %d added, content %q", full.Added, full.Content)
	}
	if status := mustRunGit(t, g.worktreePath, "status", "--porcelain"); strings.TrimSpace(status) != "?? new.txt" {
		t.Fatalf("the agent's index was changed: git status = %q", status)
	}
}
