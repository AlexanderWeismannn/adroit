package app

import (
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return m.hideMessageAfter(messageLifetime(err.Error()))
}

// hideMessageAfter numbers the message just shown and clears it after d, unless
// another has replaced it by then.
func (m *home) hideMessageAfter(d time.Duration) tea.Cmd {
	m.errSeq++
	seq := m.errSeq
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(d):
		}
		return hideErrMsg{seq: seq}
	}
}

// handleNotice reports something that went right, on the same row and the same
// timer as an error.
func (m *home) handleNotice(notice string) tea.Cmd {
	log.InfoLog.Printf("%s", notice)
	m.errBox.SetNotice(notice)
	m.recordNotice(notice)
	return m.hideMessageAfter(messageLifetime(notice))
}

// recordNotice keeps the notice for the status column, against the session
// it was about -- the selected one, which is what every notice is raised for.
// The last ten, newest first.
func (m *home) recordNotice(notice string) {
	if m.list == nil {
		return
	}
	selected := m.list.GetSelectedInstance()
	if selected == nil {
		return
	}
	if m.notices == nil {
		m.notices = map[string][]ui.Notice{}
	}
	const keep = 10
	entries := append([]ui.Notice{{Text: notice, At: time.Now()}}, m.notices[selected.Title]...)
	if len(entries) > keep {
		entries = entries[:keep]
	}
	m.notices[selected.Title] = entries
}
