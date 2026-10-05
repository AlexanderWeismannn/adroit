package app

import (
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// attentionEvent names why a session now wants you, or "" if it does not:
// either it has just stopped on a question, or it has just finished its turn.
// Both are transitions, judged against what the session was before this tick,
// so a session that stays waiting announces itself once, not every half second.
func attentionEvent(inst *session.Instance, before session.Activity, wasDone time.Time) string {
	switch {
	case inst.GetActivity() == session.ActivityNeedsInput && before != session.ActivityNeedsInput:
		return "needs-input"
	case wasDone.IsZero() && !inst.DoneAt().IsZero():
		return "finished"
	}
	return ""
}

type attention struct{ title, event string }

// notifyCmd rings the bell once for the whole tick, however many sessions
// changed in it, and runs the configured command once per session.
func (m *home) notifyCmd(events []attention) tea.Cmd {
	if len(events) == 0 {
		return nil
	}
	bell := m.appConfig.BellEnabled()
	command := m.appConfig.NotifyCommand
	if !bell && command == "" {
		return nil
	}
	return func() tea.Msg {
		if bell {
			// BEL is a C0 control, executed where it lands without disturbing the
			// frame around it, so it can go straight to the terminal.
			_, _ = os.Stdout.Write([]byte{'\a'})
		}
		for _, e := range events {
			if command == "" {
				break
			}
			c := exec.Command("sh", "-c", command)
			c.Env = append(os.Environ(), "ADROIT_SESSION="+e.title, "ADROIT_EVENT="+e.event)
			if err := c.Start(); err != nil {
				log.WarningLog.Printf("notify_command could not start: %v", err)
				break
			}
			go func() { _ = c.Wait() }()
		}
		return nil
	}
}
