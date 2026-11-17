package session

import (
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
