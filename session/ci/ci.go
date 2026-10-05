// Package ci reports the CI verdict for a session's branch, so the instance
// list can show whether that branch's pull request is passing without leaving
// the TUI.
//
// Everything here is best-effort by design: cs is useful without it, so a
// missing or unauthenticated gh CLI, a branch with no pull request, or a failing
// lookup all degrade to "no badge" rather than an error the user has to dismiss.
package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// State is the overall verdict for a branch's CI checks.
type State int

const (
	// StateUnknown means no verdict is available yet: no lookup has completed,
	// gh is unavailable, or the pull request reported no classifiable checks.
	StateUnknown State = iota
	// StateNoPR means the branch has no open pull request.
	StateNoPR
	// StatePending means at least one check is queued or running, and none failed.
	StatePending
	// StateFailure means at least one check concluded in failure.
	StateFailure
	// StateSuccess means every check that reached a conclusion passed.
	StateSuccess
	// StateMerged means the pull request was merged. Checked before the individual
	// checks, since a merged PR's checks are all green and would otherwise be
	// indistinguishable from an open branch that is merely passing.
	StateMerged
	// StateClosed means the pull request was closed without being merged.
	StateClosed
)

// Mergeability is whether a pull request can be merged into its base as it
// stands. GitHub computes this asynchronously after every push, so a pull
// request that was only just updated legitimately reports neither answer.
type Mergeability int

const (
	// MergeUnknown means GitHub has not answered, or has not answered yet.
	MergeUnknown Mergeability = iota
	// MergeClean means the pull request merges without conflict.
	MergeClean
	// MergeConflicting means the branch conflicts with its base and cannot be
	// merged until it is rebased or merged down.
	MergeConflicting
)

// ReviewState is the aggregate verdict of a pull request's reviewers.
type ReviewState int

const (
	// ReviewNone means nobody has reviewed yet, or a review is not required.
	// This is the ordinary state of a fresh pull request, so it earns no marker.
	ReviewNone ReviewState = iota
	// ReviewApproved means the pull request has an approving review and no
	// outstanding request for changes.
	ReviewApproved
	// ReviewChangesRequested means a reviewer asked for changes.
	ReviewChangesRequested
)

// Status is the CI verdict for one branch, plus the counts it was derived from.
type Status struct {
	State    State
	PRNumber int
	Passed   int
	Failed   int
	Pending  int
	// Mergeable and Review describe the pull request rather than its checks.
	// They come back in the same lookup, so they cost nothing extra, and both
	// block a merge in ways no check reports: a branch can be green and still
	// unmergeable.
	Mergeable Mergeability
	Review    ReviewState
	// Feedback is whether anyone has left remarks, and whether they are newer
	// than the branch's last commit.
	Feedback Feedback
	// FetchedAt is when the most recent lookup attempt completed. The zero value
	// means no attempt has completed yet.
	FetchedAt time.Time
}

const (
	// ttl is how long a verdict is served before a refresh is triggered. Chosen
	// to be far slower than the 500ms metadata tick that reads it: a GitHub round
	// trip per instance per tick would be both rate-limit-hostile and pointless,
	// since a workflow run takes minutes.
	ttl = 30 * time.Second
	// settledTTL is the ttl for a pull request that is merged or closed. Those
	// verdicts are final in all but the rare reopen, and each lookup is a heavy
	// GraphQL query -- checks, reviews, comments -- against the same quota as
	// your own gh use; a merged session left in the list used to cost 120 of
	// them an hour, forever.
	settledTTL = 10 * time.Minute
	// fetchTimeout bounds a single gh invocation so a hung network call cannot
	// pin a cache entry in the "fetching" state forever.
	fetchTimeout = 10 * time.Second
)

type entry struct {
	status Status
	// fetching guards against a second refresh being launched for a branch whose
	// first one is still in flight — the reader runs every 500ms and a fetch
	// takes ~1s, so without this every stale entry would spawn a pile of them.
	fetching bool
	// failing is whether the last lookup failed, so a lookup that keeps failing
	// is logged when it starts to, not every ttl.
	failing bool
}

// ttlFor is how long a verdict is served before it is refreshed.
func ttlFor(s Status) time.Duration {
	if s.State == StateMerged || s.State == StateClosed {
		return settledTTL
	}
	return ttl
}

var (
	mu     sync.Mutex
	cache  = make(map[string]entry)
	ghPath string
	// disabled latches once we learn gh cannot answer at all (absent, or not
	// authenticated). Without it a broken auth setup logs a warning per instance
	// every ttl, forever.
	disabled bool
	lookOnce sync.Once
)

// Get returns the last known CI status for a session's worktree, triggering a
// background refresh when the cached verdict is older than ttl.
//
// Keyed on the worktree rather than a branch name, because the branch is resolved
// at refresh time from the worktree's HEAD (see refresh): a session that switched
// or renamed its branch must not keep reporting the old one's verdict, and it must
// not need a second cache entry to stop doing so.
//
// It never blocks on the network or on git. The caller is the UI's metadata tick,
// which also computes git diffs and only re-schedules once complete, so a slow gh
// call here would directly stall diff-stat updates. The first call for a worktree
// therefore returns the zero Status and the badge appears a tick or two later.
func Get(repoPath, worktreePath, fallbackBranch string) Status {
	if repoPath == "" || worktreePath == "" || !available() {
		return Status{}
	}

	key := repoPath + "\x00" + worktreePath

	mu.Lock()
	defer mu.Unlock()
	if len(cache) > 1 {
		sweepLocked()
	}
	e := cache[key]
	if time.Since(e.status.FetchedAt) > ttlFor(e.status) && !e.fetching {
		e.fetching = true
		cache[key] = e
		go refresh(key, repoPath, worktreePath, fallbackBranch)
	}
	return e.status
}

// Forget drops a worktree's cached verdict. Called when a session is killed:
// the cache is keyed on a path that no longer exists, and nothing else would
// ever evict it.
func Forget(repoPath, worktreePath string) {
	if repoPath == "" || worktreePath == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	delete(cache, repoPath+"\x00"+worktreePath)
}

// sweepAge is how long an untouched entry survives. A backstop under Forget, for
// the paths that never announce their own end -- a worktree removed by hand, a
// session whose title changed.
const sweepAge = time.Hour

// sweepLocked drops entries nothing has asked about in sweepAge. Called from
// Get, which is the only thing that grows the map.
func sweepLocked() {
	for k, e := range cache {
		if time.Since(e.status.FetchedAt) > sweepAge && !e.fetching {
			delete(cache, k)
		}
	}
}

// refresh resolves the worktree's current branch and performs one lookup.
//
// The branch comes from HEAD rather than from the name the session was created
// with, so that renaming the branch or checking out another one inside the
// worktree is reflected. The recorded name is the fallback for a detached HEAD or
// a worktree that has gone away.
func refresh(key, repoPath, worktreePath, fallbackBranch string) {
	branch, err := git.CurrentBranch(worktreePath)
	if err != nil || branch == "" {
		branch = fallbackBranch
	}

	// Read locally: it costs a git call we are already in the neighbourhood of,
	// where asking gh for the pull request's commits would more than double the
	// response size for one timestamp. A failure here is not fatal -- it only
	// means feedback cannot be dated.
	headTime, headErr := git.HeadCommitTime(worktreePath)
	if headErr != nil {
		log.WarningLog.Printf("could not read HEAD time at %s: %v", worktreePath, headErr)
	}

	var status Status
	if branch == "" {
		err = errors.New("no branch to look up")
	} else {
		status, err = fetch(repoPath, branch, headTime)
	}

	mu.Lock()
	defer mu.Unlock()
	e := cache[key]
	e.fetching = false
	if err != nil {
		// Keep whatever verdict we had rather than blanking the badge on a
		// transient failure, but stamp the attempt so the next retry waits a
		// full ttl instead of firing on the next tick.
		e.status.FetchedAt = time.Now()
		if !e.failing {
			log.WarningLog.Printf("could not fetch CI status for branch %q: %v", branch, err)
		}
		e.failing = true
		cache[key] = e
		return
	}
	e.status = status
	e.failing = false
	cache[key] = e
}

// rollupEntry is the subset of a statusCheckRollup element that carries an
// outcome. A CheckRun (GitHub Actions and most apps) reports `conclusion` once
// finished and `status` while running; a legacy StatusContext — some deploy and
// coverage bots still post these — reports only `state`.
type rollupEntry struct {
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	State      string `json:"state"`
}

// Feedback is whether a pull request carries review or conversation comments,
// and whether they landed after the branch's last commit.
//
// A separate axis from ReviewState, not another value of it, because they answer
// different questions and co-occur: a pull request is routinely approved AND
// carrying unresolved remarks. ReviewState is GitHub's own reviewDecision, which
// only moves on an approval or a request for changes -- a review submitted as
// COMMENTED, which is what every review bot here does, leaves it empty. So a
// pull request with real feedback on it renders identically to one nobody has
// opened, which is the gap this closes.
type Feedback int

const (
	// FeedbackNone: nothing has been said.
	FeedbackNone Feedback = iota
	// FeedbackAddressed: there are remarks, but the branch has moved since the
	// last of them, so they are presumed answered.
	FeedbackAddressed
	// FeedbackOutstanding: the last remark is newer than the branch's last
	// commit -- someone has said something you have not built on yet.
	FeedbackOutstanding
)

// reviewEntry is the subset of a review that says what it was and when.
type reviewEntry struct {
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// commentEntry is the subset of a conversation comment that says when.
type commentEntry struct {
	CreatedAt time.Time `json:"createdAt"`
}

type prView struct {
	Number            int           `json:"number"`
	State             string        `json:"state"`
	Mergeable         string        `json:"mergeable"`
	ReviewDecision    string        `json:"reviewDecision"`
	StatusCheckRollup []rollupEntry `json:"statusCheckRollup"`
	// latestReviews rather than reviews: one entry per reviewer instead of every
	// review ever submitted, which is materially smaller on a long-lived pull
	// request and carries the same answer, since only the most recent remark
	// from anyone can still be outstanding.
	LatestReviews []reviewEntry  `json:"latestReviews"`
	Comments      []commentEntry `json:"comments"`
}

// lastRemark returns when the most recent piece of feedback landed, and whether
// there was any.
//
// An approval is not feedback: it is already carried by ReviewState's own marker,
// and counting it would light the comment marker on every approved pull request.
func (v prView) lastRemark() (time.Time, bool) {
	var latest time.Time
	for _, r := range v.LatestReviews {
		if strings.EqualFold(r.State, "APPROVED") {
			continue
		}
		if r.SubmittedAt.After(latest) {
			latest = r.SubmittedAt
		}
	}
	for _, c := range v.Comments {
		if c.CreatedAt.After(latest) {
			latest = c.CreatedAt
		}
	}
	return latest, !latest.IsZero()
}

// fetch runs gh in repoPath and classifies the result.
func fetch(repoPath, branch string, headTime time.Time) (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ghPath, "pr", "view", branch,
		"--json", "number,state,mergeable,reviewDecision,statusCheckRollup,latestReviews,comments")
	// Run inside the repo so gh resolves the remote itself; this also means a
	// fork or a non-github remote simply yields an error we swallow.
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		stderr := stderrOf(err)
		// gh exits non-zero for a branch with no pull request. That is a verdict,
		// not a failure — and the common case for a session whose work is not
		// pushed yet, so it must not be logged as an error every ttl.
		if strings.Contains(stderr, "no pull requests found") {
			return Status{State: StateNoPR, FetchedAt: time.Now()}, nil
		}
		// An auth problem is not per-branch and will not fix itself; stop asking.
		if strings.Contains(stderr, "gh auth login") {
			disable("gh is not authenticated")
			return Status{}, errors.New("gh is not authenticated")
		}
		return Status{}, fmt.Errorf("gh pr view: %w: %s", err, strings.TrimSpace(stderr))
	}

	var view prView
	if err := json.Unmarshal(out, &view); err != nil {
		return Status{}, fmt.Errorf("parsing gh output: %w", err)
	}

	if terminal := terminalState(view.State); terminal != StateUnknown {
		return Status{State: terminal, PRNumber: view.Number, FetchedAt: time.Now()}, nil
	}

	status := classify(view.StatusCheckRollup)
	status.PRNumber = view.Number
	status.Mergeable = mergeability(view.Mergeable)
	status.Review = reviewState(view.ReviewDecision)
	status.Feedback = feedbackState(view, headTime)
	status.FetchedAt = time.Now()
	return status, nil
}

// feedbackState dates the last remark against the branch's last commit.
//
// With no head time -- an unreadable worktree -- feedback is reported as merely
// present rather than outstanding: claiming it is unanswered is a stronger
// statement than the evidence supports.
func feedbackState(v prView, headTime time.Time) Feedback {
	last, any := v.lastRemark()
	if !any {
		return FeedbackNone
	}
	if headTime.IsZero() || !last.After(headTime) {
		return FeedbackAddressed
	}
	return FeedbackOutstanding
}

// terminalState maps a pull request's own state to a verdict, for the states where
// the PR is the answer and its checks are not.
//
// Every check on a merged PR is green, so letting classify decide would render a
// merged branch identically to an open one that is simply passing -- and a
// closed-unmerged PR would likewise show a green tick for abandoned work. Returns
// StateUnknown for an open PR, meaning "let the checks decide".
func terminalState(prState string) State {
	switch strings.ToUpper(prState) {
	case "MERGED":
		return StateMerged
	case "CLOSED":
		return StateClosed
	default:
		return StateUnknown
	}
}

// mergeability maps GitHub's mergeable field to a verdict.
//
// Only an explicit CONFLICTING is treated as a conflict. GitHub computes
// mergeability asynchronously and reports UNKNOWN in the meantime, which is the
// normal state for the seconds after a push -- reading that as a conflict would
// make every branch flash a blocker it does not have.
func mergeability(value string) Mergeability {
	switch strings.ToUpper(value) {
	case "CONFLICTING":
		return MergeConflicting
	case "MERGEABLE":
		return MergeClean
	default:
		return MergeUnknown
	}
}

// reviewState maps GitHub's reviewDecision to a verdict.
//
// REVIEW_REQUIRED and the empty string both mean "nobody has said anything yet",
// which is true of every pull request at some point and so is not worth a marker.
func reviewState(value string) ReviewState {
	switch strings.ToUpper(value) {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChangesRequested
	default:
		return ReviewNone
	}
}

// classify reduces a status check rollup to a single verdict.
//
// SKIPPED and CANCELLED are ignored rather than counted as failures. A cancelled
// run is nearly always one superseded by a newer push to the same branch, and
// skipped jobs are the normal, healthy result of path filters — counting either
// as red would leave most branches permanently showing a failure.
func classify(entries []rollupEntry) Status {
	var s Status
	for _, e := range entries {
		switch outcomeOf(e) {
		case "SUCCESS", "NEUTRAL":
			s.Passed++
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "ERROR":
			s.Failed++
		case "SKIPPED", "CANCELLED", "STALE", "":
			// Carries no verdict — see the docblock.
		default:
			// QUEUED, IN_PROGRESS, PENDING, WAITING, REQUESTED, EXPECTED.
			s.Pending++
		}
	}

	// Deliberately ordered worst-first: one red check matters even while others
	// are still running, which is the whole point of glancing at the badge.
	switch {
	case s.Failed > 0:
		s.State = StateFailure
	case s.Pending > 0:
		s.State = StatePending
	case s.Passed > 0:
		s.State = StateSuccess
	default:
		s.State = StateUnknown
	}
	return s
}

// outcomeOf picks whichever field carries this entry's outcome.
func outcomeOf(e rollupEntry) string {
	if e.Conclusion != "" {
		return strings.ToUpper(e.Conclusion)
	}
	if e.Status != "" {
		return strings.ToUpper(e.Status)
	}
	return strings.ToUpper(e.State)
}

// OpenInBrowser opens a branch's pull request in the user's browser.
//
// Unlike everything else here this is a foreground action the user asked for, so
// it reports its failure rather than degrading silently: "nothing happened" is
// not an acceptable answer to a keypress. gh's own stderr is the message, since
// it already distinguishes the cases worth telling apart — no pull request for
// this branch, no browser to open one in, not authenticated.
//
// Blocks until gh returns, so callers should run it off the event loop.
func OpenInBrowser(repoPath, branch string) error {
	if !available() {
		return errors.New("cannot open the pull request: the gh CLI is unavailable or not authenticated")
	}
	if repoPath == "" || branch == "" {
		return errors.New("cannot open the pull request: no branch to look up")
	}

	mu.Lock()
	gh := ghPath
	mu.Unlock()

	cmd := exec.Command(gh, "pr", "view", branch, "--web")
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("gh pr view --web: %s", detail)
		}
		return fmt.Errorf("gh pr view --web: %w", err)
	}
	return nil
}

// available reports whether gh can be consulted. The PATH lookup happens once.
func available() bool {
	lookOnce.Do(func() {
		path, err := exec.LookPath("gh")
		if err != nil {
			disable("gh CLI not found on PATH")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		ghPath = path
	})

	mu.Lock()
	defer mu.Unlock()
	return !disabled && ghPath != ""
}

// disable latches the feature off for the rest of the process, logging why once.
func disable(reason string) {
	mu.Lock()
	defer mu.Unlock()
	if disabled {
		return
	}
	disabled = true
	log.WarningLog.Printf("GitHub CI status badges disabled: %s", reason)
}

func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return err.Error()
}
