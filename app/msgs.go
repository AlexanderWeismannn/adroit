package app

import (
	"time"

	"github.com/AlexanderWeismannn/adroit/session"
)

// devStackStoppedMsg reports the outcome of tearing the stack down.
type devStackStoppedMsg struct {
	err error
}

// devStackStartedMsg reports the outcome of pointing the stack at a session.
type devStackStartedMsg struct {
	title string
	err   error
}

// hideErrMsg implements tea.Msg and clears the error text from the screen, if
// it is still the message numbered seq.
type hideErrMsg struct{ seq int }

// messageLifetime is how long a message stays: long enough to read. A flat 3s
// was too short for the teardown errors, which run to a sentence or two.
func messageLifetime(text string) time.Duration {
	d := 3*time.Second + time.Duration(len(text))*40*time.Millisecond
	return min(d, 10*time.Second)
}

// killApprovedMsg carries a session that has passed the checks a kill can fail,
// back onto the update loop so the model can be changed before anything is torn
// down.
type killApprovedMsg struct {
	instance *session.Instance
}

// killFinishedMsg reports a teardown that ran off the loop. The session is gone
// from the list either way by the time this arrives; what it can still carry is
// a worktree or a tmux session that would not go quietly.
type killFinishedMsg struct {
	title string
	err   error
}

type instanceChangedMsg struct{}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

// runUpdateMsg asks the update loop to start bringing a worktree up to date.
//
// The confirmation overlay runs its action on the main thread, so the action
// itself cannot do the work: a fetch and a merge are seconds of network and disk
// during which the interface would be frozen. It returns this instead, and the
// loop turns it into a background command.
type runUpdateMsg struct {
	instance *session.Instance
	// reset discards the worktree's own commits and takes the remote's history
	// wholesale, which is the only way past a divergence.
	reset bool
}

// updateDoneMsg is sent when a worktree has been brought up to date, or failed
// to be.
type updateDoneMsg struct {
	err error
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// instanceStartDoneMsg is sent when the background instance start completes.
type instanceStartDoneMsg struct {
	instance *session.Instance
	err      error
}

// instanceResumedMsg and instancePausedMsg carry the result of a resume or a
// checkout's pause back from offLoop.
type instanceResumedMsg struct {
	instance *session.Instance
	err      error
}

type instancePausedMsg struct {
	instance *session.Instance
	err      error
}

// noticeMsg carries a notice back from work done off the loop.
type noticeMsg string
