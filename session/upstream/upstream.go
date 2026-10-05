// Package upstream reports how a session's branch stands relative to its remote
// counterpart, so the list can say when someone else has pushed to a branch you
// are reviewing, and a keypress can bring those commits in.
//
// A worktree is a plain checkout that nothing updates on its own: created once
// from whatever the branch pointed at then, it stays there for the life of the
// session. Reviewing a branch someone is still pushing to therefore reads an
// increasingly old copy, with nothing on screen to say so -- which is the gap
// this closes.
//
// Everything here is best-effort, on the ci package's model: a branch with no
// remote counterpart, an offline box or a failing git call all degrade to "no
// badge" rather than an error to dismiss.
package upstream

import (
	"context"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
)

// Status is how one worktree stands against its remote-tracking ref.
type Status struct {
	// Ref is the remote-tracking ref the counts are measured against, e.g.
	// "origin/TASK-1234". Empty means the branch has no remote counterpart, which
	// is the ordinary state of a session's own fresh branch and earns no badge.
	Ref string
	// Ahead is commits this worktree has that the remote does not; Behind is the
	// reverse. Both non-zero means the histories have parted company -- typically
	// a force-push upstream, which no pull can reconcile.
	Ahead  int
	Behind int
	// FetchedAt is when the most recent refresh completed. The zero value means
	// none has.
	FetchedAt time.Time
}

// Tracked reports whether there is a remote counterpart to compare against.
func (s Status) Tracked() bool { return s.Ref != "" }

// Diverged reports whether the two histories have parted company, so that
// bringing the remote in means discarding local commits rather than fast
// forwarding onto them.
func (s Status) Diverged() bool { return s.Ahead > 0 && s.Behind > 0 }

// Stale reports whether the remote has commits this worktree does not -- the one
// fact the whole package exists to surface.
func (s Status) Stale() bool { return s.Behind > 0 }

const (
	// ttl is how long a count is served before a refresh is triggered. The
	// counts are local and cheap; what they are measured against is only as
	// fresh as the last fetch, which fetchInterval governs.
	ttl = 20 * time.Second
	// fetchInterval is the minimum gap between two network fetches of one
	// repository, and so the real ceiling on how stale a badge can be.
	//
	// Per REPOSITORY, not per worktree: every session on a repo shares its object
	// store and its remote-tracking refs, so one `git fetch` answers for all of
	// them. Without the gate a box with eight sessions on one repo would fire
	// eight fetches per ttl to learn the same thing eight times.
	fetchInterval = 60 * time.Second
	// fetchTimeout bounds one fetch so a hung network call cannot pin a cache
	// entry in the fetching state forever.
	fetchTimeout = 20 * time.Second
	// gitTimeout bounds the local rev-list and rev-parse calls.
	gitTimeout = 5 * time.Second
)

type entry struct {
	status Status
	// fetching guards against a second refresh for a worktree whose first is
	// still in flight: the reader runs every 500ms and a refresh can take
	// seconds, so without it every stale entry would spawn a pile of them.
	fetching bool
}

type repoFetch struct {
	last     time.Time
	inFlight bool
}

var (
	mu    sync.Mutex
	cache = make(map[string]entry)
	// fetches gates network access per repository -- see fetchInterval.
	fetches = make(map[string]*repoFetch)
)

// Get returns the last known upstream standing for a worktree, triggering a
// background refresh when the cached counts are older than ttl.
//
// It never blocks on the network or on git. The caller is the UI's metadata
// tick, which computes diffs on the same pass and only re-schedules once
// everything completes, so a fetch on this goroutine would stall diff updates
// outright. The first call for a worktree therefore returns the zero Status and
// the badge appears a tick or two later.
func Get(repoPath, worktreePath, fallbackBranch string) Status {
	if repoPath == "" || worktreePath == "" {
		return Status{}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cache) > 1 {
		sweepLocked()
	}
	e := cache[worktreePath]
	if time.Since(e.status.FetchedAt) > ttl && !e.fetching {
		e.fetching = true
		cache[worktreePath] = e
		go refresh(repoPath, worktreePath, fallbackBranch)
	}
	return e.status
}

// Peek returns the cached standing without triggering anything. For callers
// deciding what a keypress should do, where launching a refresh would answer too
// late to matter.
func Peek(worktreePath string) Status {
	mu.Lock()
	defer mu.Unlock()
	return cache[worktreePath].status
}

// sweepAge is how long an untouched entry survives, as a backstop under Forget:
// a worktree removed by hand announces nothing, and neither map would ever
// otherwise shrink.
const sweepAge = time.Hour

// sweepLocked drops cache and gate entries nothing has asked about in sweepAge.
func sweepLocked() {
	for k, e := range cache {
		if time.Since(e.status.FetchedAt) > sweepAge && !e.fetching {
			delete(cache, k)
		}
	}
	for k, f := range fetches {
		if time.Since(f.last) > sweepAge && !f.inFlight {
			delete(fetches, k)
		}
	}
}

// Forget drops a worktree's cached counts for good. Called when a session is
// killed, where Invalidate's "recompute next tick" is the wrong answer: there
// will be no next tick, and the path is gone.
func Forget(worktreePath string) {
	mu.Lock()
	defer mu.Unlock()
	delete(cache, worktreePath)
}

// Invalidate drops a worktree's cached counts so the next tick recomputes them.
// Called after this process moves the branch itself -- a pull that left the
// badge reading "3 behind" for another twenty seconds would look like it failed.
func Invalidate(worktreePath string) {
	mu.Lock()
	defer mu.Unlock()
	delete(cache, worktreePath)
}

func refresh(repoPath, worktreePath, fallbackBranch string) {
	maybeFetch(repoPath)
	status, err := measure(worktreePath, fallbackBranch)

	mu.Lock()
	defer mu.Unlock()
	e := cache[worktreePath]
	e.fetching = false
	if err != nil {
		// Keep the counts we had rather than blanking the badge on a transient
		// failure, but stamp the attempt so the retry waits a full ttl instead of
		// firing again on the next tick.
		e.status.FetchedAt = time.Now()
		cache[worktreePath] = e
		log.WarningLog.Printf("could not read upstream status at %s: %v", worktreePath, err)
		return
	}
	status.FetchedAt = time.Now()
	e.status = status
	cache[worktreePath] = e
}

// measure resolves the worktree's remote-tracking ref and counts both ways
// against it, without touching the network.
func measure(worktreePath, fallbackBranch string) (Status, error) {
	ref := resolveRef(worktreePath, fallbackBranch)
	if ref == "" {
		// Not an error: a session's own new branch has no remote counterpart until
		// it is pushed, and that is the majority of rows.
		return Status{}, nil
	}
	ahead, behind, err := count(worktreePath, ref)
	if err != nil {
		return Status{}, err
	}
	return Status{Ref: ref, Ahead: ahead, Behind: behind}, nil
}

// resolveRef names the remote-tracking ref to compare against, or "" when there
// is none.
//
// The configured upstream comes first, because it is what the user's own `git
// pull` would use, and a branch tracking a fork or a differently named remote
// branch must not be measured against a coincidental origin/<name>. The fallback
// covers the worktrees adroit creates from a local branch, which git leaves with
// no upstream configured at all.
func resolveRef(worktreePath, fallbackBranch string) string {
	if out, err := run(worktreePath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}

	branch := fallbackBranch
	if out, err := run(worktreePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if head := strings.TrimSpace(out); head != "" && head != "HEAD" {
			// The branch actually checked out, not the one recorded at creation: a
			// session whose branch was switched or renamed inside the worktree must
			// not be measured against the old one's remote.
			branch = head
		}
	}
	if branch == "" {
		return ""
	}
	ref := "origin/" + branch
	if _, err := run(worktreePath, "show-ref", "--verify", "refs/remotes/"+ref); err != nil {
		return ""
	}
	return ref
}

// count returns how many commits each side has that the other does not.
func count(worktreePath, ref string) (ahead, behind int, err error) {
	out, err := run(worktreePath, "rev-list", "--left-right", "--count", ref+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	// Left is the ref, right is HEAD, so left is what we are missing.
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", strings.TrimSpace(out))
	}
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q: %w", strings.TrimSpace(out), err)
	}
	if ahead, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q: %w", strings.TrimSpace(out), err)
	}
	return ahead, behind, nil
}

// maybeFetch updates a repository's remote-tracking refs, at most once per
// fetchInterval and never twice at once. Failures are logged and ignored: an
// offline box should keep showing the counts it last knew, not blank them.
func maybeFetch(repoPath string) {
	mu.Lock()
	f := fetches[repoPath]
	if f == nil {
		f = &repoFetch{}
		fetches[repoPath] = f
	}
	if f.inFlight || time.Since(f.last) < fetchInterval {
		mu.Unlock()
		return
	}
	f.inFlight = true
	mu.Unlock()

	err := Fetch(repoPath)

	mu.Lock()
	f.inFlight = false
	f.last = time.Now()
	mu.Unlock()

	if err != nil {
		log.WarningLog.Printf("could not fetch %s: %v", repoPath, err)
	}
}

// Fetch updates every remote-tracking ref in a repository, ungated. Exported for
// the actions that must not act on a stale ref: telling someone their branch is
// three commits behind and then merging what we fetched a minute ago is how a
// review ends up looking at the wrong code.
func Fetch(repoPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	// --prune so a branch deleted upstream stops being compared against a ref
	// that no longer exists anywhere but here.
	fetch := cmd.NonInteractiveGit(exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--prune", "--quiet"))
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Pull fast-forwards a worktree onto its remote-tracking ref, after refreshing
// that ref, and returns the standing that results.
//
// --ff-only rather than a plain `git pull`: a merge commit synthesised into
// somebody else's branch, in a worktree you opened to read it, is not a thing
// this key should be able to do. Divergence is reported for the caller to handle
// deliberately.
func Pull(repoPath, worktreePath, fallbackBranch string) (Status, error) {
	defer Invalidate(worktreePath)

	if err := Fetch(repoPath); err != nil {
		return Status{}, err
	}
	status, err := measure(worktreePath, fallbackBranch)
	if err != nil {
		return Status{}, err
	}
	switch {
	case !status.Tracked():
		return status, fmt.Errorf("no remote branch to update from")
	case status.Diverged():
		return status, fmt.Errorf("%s has diverged: %d local commit(s) the remote does not have, %d it does",
			status.Ref, status.Ahead, status.Behind)
	case !status.Stale():
		return status, nil
	}
	if out, err := run(worktreePath, "merge", "--ff-only", status.Ref); err != nil {
		return status, fmt.Errorf("could not fast-forward onto %s: %s", status.Ref, firstLine(out, err))
	}
	return measure(worktreePath, fallbackBranch)
}

// Reset discards the worktree's local commits and takes the remote's history
// wholesale. The answer to an upstream force-push, which is why Pull refuses to
// do it silently.
//
// Refuses on a dirty worktree: a hard reset destroys uncommitted work with
// nothing to recover it from, and a reset asked for to resync a review is never
// a request to throw away edits.
func Reset(repoPath, worktreePath, fallbackBranch string) (Status, error) {
	defer Invalidate(worktreePath)

	dirty, err := IsDirty(worktreePath)
	if err != nil {
		return Status{}, err
	}
	if dirty {
		return Status{}, fmt.Errorf("worktree has uncommitted changes -- commit or stash them before resetting")
	}
	if err := Fetch(repoPath); err != nil {
		return Status{}, err
	}
	status, err := measure(worktreePath, fallbackBranch)
	if err != nil {
		return Status{}, err
	}
	if !status.Tracked() {
		return status, fmt.Errorf("no remote branch to reset to")
	}
	if out, err := run(worktreePath, "reset", "--hard", status.Ref); err != nil {
		return status, fmt.Errorf("could not reset to %s: %s", status.Ref, firstLine(out, err))
	}
	return measure(worktreePath, fallbackBranch)
}

// IsDirty reports whether a worktree has uncommitted changes, untracked files
// included.
func IsDirty(worktreePath string) (bool, error) {
	out, err := run(worktreePath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("could not read git status at %s: %w", worktreePath, err)
	}
	return strings.TrimSpace(out) != "", nil
}

func run(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := cmd.NonInteractiveGit(exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)).CombinedOutput()
	return string(out), err
}

// firstLine picks the line of git's output worth putting in an error box. Git
// prefaces the useful sentence with several lines of advice on a failed merge,
// and the box is one line high.
func firstLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}
