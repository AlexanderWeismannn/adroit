package upstream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0644))
	git(t, dir, "add", name)
	git(t, dir, "commit", "-m", "add "+name)
}

// fixture builds a bare origin with branch "review" on it, and two clones of it:
// mine, standing in for a session's worktree, and theirs, standing in for the
// person still pushing to the branch.
func fixture(t *testing.T) (mine, theirs string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "--bare", "--initial-branch=main", origin)

	seed := filepath.Join(root, "seed")
	git(t, root, "clone", origin, seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.com")
	commit(t, seed, "README.md", "hello\n")
	git(t, seed, "checkout", "-b", "review")
	git(t, seed, "push", "-u", "origin", "review")

	for _, name := range []string{"mine", "theirs"} {
		path := filepath.Join(root, name)
		git(t, root, "clone", "--branch", "review", origin, path)
		git(t, path, "config", "user.name", "Test User")
		git(t, path, "config", "user.email", "test@example.com")
	}
	return filepath.Join(root, "mine"), filepath.Join(root, "theirs")
}

// The zero case, and the one every session starts in: level with the remote, so
// nothing to say and no badge to draw.
func TestMeasureReportsLevelWithTheRemote(t *testing.T) {
	mine, _ := fixture(t)

	status, err := measure(mine, "review")
	require.NoError(t, err)
	require.Equal(t, "origin/review", status.Ref)
	require.Equal(t, 0, status.Ahead)
	require.Equal(t, 0, status.Behind)
	require.False(t, status.Stale())
	require.False(t, status.Diverged())
}

// A branch that exists only here has no remote counterpart to be behind, which
// is the ordinary state of a session's own new branch. Reported as untracked
// rather than as an error: it is not a failure, and it must not draw a badge.
func TestMeasureReportsNoRefForABranchThatIsNotOnTheRemote(t *testing.T) {
	mine, _ := fixture(t)
	git(t, mine, "checkout", "-b", "local-only")

	status, err := measure(mine, "local-only")
	require.NoError(t, err)
	require.False(t, status.Tracked())
	require.Empty(t, status.Ref)
}

// The case the feature exists for: someone else pushed while a session sat on
// the branch. The count must appear only once the remote-tracking ref is
// refreshed, since that is what the badge is measured against.
func TestFetchIsWhatMakesTheirCommitsVisible(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")

	before, err := measure(mine, "review")
	require.NoError(t, err)
	require.Equal(t, 0, before.Behind, "not fetched yet, so nothing is known")

	require.NoError(t, Fetch(mine))

	after, err := measure(mine, "review")
	require.NoError(t, err)
	require.Equal(t, 1, after.Behind)
	require.Equal(t, 0, after.Ahead)
	require.True(t, after.Stale())
	require.False(t, after.Diverged())
}

// Unpushed work of our own is the other direction, and is not staleness: the
// checkout is not behind anything.
func TestMeasureCountsOurOwnUnpushedCommits(t *testing.T) {
	mine, _ := fixture(t)
	commit(t, mine, "mine.txt", "one\n")

	status, err := measure(mine, "review")
	require.NoError(t, err)
	require.Equal(t, 1, status.Ahead)
	require.Equal(t, 0, status.Behind)
	require.False(t, status.Stale())
}

// Both directions at once. The distinction is load-bearing: this is the one
// state no pull can resolve, so the action offered for it has to be a different
// action.
func TestMeasureReportsDivergence(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")
	commit(t, mine, "mine.txt", "one\n")
	require.NoError(t, Fetch(mine))

	status, err := measure(mine, "review")
	require.NoError(t, err)
	require.Equal(t, 1, status.Ahead)
	require.Equal(t, 1, status.Behind)
	require.True(t, status.Diverged())
	require.True(t, status.Stale())
}

// The upstream a branch actually tracks wins over origin/<name>, so a branch
// following a fork or a differently named remote branch is not measured against
// a coincidental namesake.
func TestResolveRefPrefersTheConfiguredUpstream(t *testing.T) {
	mine, _ := fixture(t)
	git(t, mine, "checkout", "-b", "renamed", "origin/review")

	require.Equal(t, "origin/review", resolveRef(mine, "renamed"))
}

// Worktrees adroit creates from an existing local branch have no upstream
// configured at all, so the fallback is what makes the badge work for the
// sessions it was built for.
func TestResolveRefFallsBackToOriginForAnUntrackedBranch(t *testing.T) {
	mine, _ := fixture(t)
	git(t, mine, "branch", "--unset-upstream")

	require.Equal(t, "origin/review", resolveRef(mine, "review"))
}

// A detached HEAD names no branch, and the recorded name is what is left to go
// on.
func TestResolveRefUsesTheRecordedBranchOnADetachedHead(t *testing.T) {
	mine, _ := fixture(t)
	git(t, mine, "checkout", "--detach")

	require.Equal(t, "origin/review", resolveRef(mine, "review"))
}

// Pull fetches first and reports the standing it produced, so a caller can say
// what happened without a second round of git.
func TestPullFastForwardsOntoTheRemote(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")

	status, err := Pull(mine, mine, "review")
	require.NoError(t, err)
	require.Equal(t, 0, status.Behind)
	require.Equal(t, 0, status.Ahead)
	require.FileExists(t, filepath.Join(mine, "theirs.txt"), "the pulled commit is in the tree")
}

// Nothing to do is not a failure: the key can be pressed on a row whose badge
// has only just cleared.
func TestPullOnALevelBranchIsANoop(t *testing.T) {
	mine, _ := fixture(t)

	status, err := Pull(mine, mine, "review")
	require.NoError(t, err)
	require.Equal(t, 0, status.Behind)
}

// Pull is --ff-only by design: a merge commit synthesised into somebody else's
// branch is not something a keypress in a session list should be able to do.
func TestPullRefusesToReconcileADivergedBranch(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")
	commit(t, mine, "mine.txt", "one\n")

	head := git(t, mine, "rev-parse", "HEAD")
	status, err := Pull(mine, mine, "review")
	require.Error(t, err)
	require.Contains(t, err.Error(), "diverged")
	require.True(t, status.Diverged(), "the counts come back so the caller can name them")
	require.Equal(t, head, git(t, mine, "rev-parse", "HEAD"), "and nothing moved")
	require.NoFileExists(t, filepath.Join(mine, "theirs.txt"))
}

// Reset is the way past a divergence, and it does what Pull refuses to: takes
// the remote's history and abandons ours.
func TestResetTakesTheRemoteHistory(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")
	commit(t, mine, "mine.txt", "one\n")

	status, err := Reset(mine, mine, "review")
	require.NoError(t, err)
	require.Equal(t, 0, status.Ahead)
	require.Equal(t, 0, status.Behind)
	require.FileExists(t, filepath.Join(mine, "theirs.txt"))
	require.NoFileExists(t, filepath.Join(mine, "mine.txt"), "our commit is gone, as asked")
}

// A hard reset destroys uncommitted work with nothing to recover it from, and
// "resync this review" is never a request to throw away edits. Untracked files
// count: they are the shape stray notes and scratch scripts take.
func TestResetRefusesADirtyWorktree(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")
	require.NoError(t, os.WriteFile(filepath.Join(mine, "scratch.txt"), []byte("wip\n"), 0644))

	_, err := Reset(mine, mine, "review")
	require.Error(t, err)
	require.Contains(t, err.Error(), "uncommitted")
	require.FileExists(t, filepath.Join(mine, "scratch.txt"))
}

func TestIsDirtySeesUncommittedWork(t *testing.T) {
	mine, _ := fixture(t)

	dirty, err := IsDirty(mine)
	require.NoError(t, err)
	require.False(t, dirty)

	require.NoError(t, os.WriteFile(filepath.Join(mine, "README.md"), []byte("edited\n"), 0644))
	dirty, err = IsDirty(mine)
	require.NoError(t, err)
	require.True(t, dirty)
}

// Get is the UI's entry point and must never block on the network, so the first
// call for a worktree returns nothing and the answer arrives on a later tick.
func TestGetIsNonBlockingAndCaches(t *testing.T) {
	mine, theirs := fixture(t)
	commit(t, theirs, "theirs.txt", "one\n")
	git(t, theirs, "push")
	t.Cleanup(func() { Invalidate(mine) })

	require.Equal(t, Status{}, Get(mine, mine, "review"), "first call only schedules the lookup")

	require.Eventually(t, func() bool {
		return Get(mine, mine, "review").Behind == 1
	}, 30*time.Second, 100*time.Millisecond)
}
