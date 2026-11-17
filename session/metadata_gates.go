package session

import (
	"time"

	"github.com/AlexanderWeismannn/adroit/session/tmux"
)

// PollPaneSeen is PollPane given the tmux server's activity, read once for the
// whole list: a session whose pane has drawn nothing since its last capture is
// not captured again. seen may be nil, which captures as PollPane does.
func (i *Instance) PollPaneSeen(seen tmux.Activity) tmux.PaneState {
	if !i.started || i.Status == Paused {
		return tmux.PaneState{}
	}
	return i.tmuxSession.PollPaneSeen(seen)
}

// PaneStillFor is how long this session's pane has gone without drawing
// anything, by the same activity. False when that is not known.
func (i *Instance) PaneStillFor(seen tmux.Activity, now time.Time) (time.Duration, bool) {
	if !i.started || i.tmuxSession == nil {
		return 0, false
	}
	return seen.StillFor(i.tmuxSession, now)
}

// WorktreeChanged reports, without forking, whether git has recorded anything
// in this session's worktree -- a commit, an add, a checkout -- since its diff
// was last computed. False for a session with no worktree of its own.
func (i *Instance) WorktreeChanged() bool {
	if !i.started || i.gitWorktree == nil {
		return false
	}
	return i.gitWorktree.ChangedSinceDiff()
}
