package app

import (
	"os"
	"os/exec"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"

	tea "charm.land/bubbletea/v2"
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

// prEvent names what changed on a session's pull request between two lookups
// that wants you, or "" if nothing did: a new remark from someone else, or a
// new approval. A first lookup (before.FetchedAt zero) announces nothing, so
// starting Adroit does not ring for every remark already on every pull request.
func prEvent(before, after ci.Status) string {
	if before.FetchedAt.IsZero() || after.FetchedAt.IsZero() || before.PRNumber != after.PRNumber {
		return ""
	}
	switch {
	case after.LastRemarkAt.After(before.LastRemarkAt) && after.Feedback == ci.FeedbackOutstanding:
		if after.Review == ci.ReviewChangesRequested && before.Review != ci.ReviewChangesRequested {
			return "pr-changes-requested"
		}
		return "pr-comment"
	case after.Review == ci.ReviewApproved && before.Review != ci.ReviewApproved:
		return "pr-approved"
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
