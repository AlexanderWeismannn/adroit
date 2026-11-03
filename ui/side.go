package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/theme"
)

// Side is the status column of the wide layout: everything known about the
// selected session that the row and the tab bar used to carry in glyphs,
// spelled out.
//
//	╭─ TASK-6664 ──────────────────╮
//	│ Branch                        │
//	│  ⎇ TASK-6664                  │
//	│  base main · ↑4 ↓0            │
//	│                               │
//	│ Pull request                  │
//	│  #5339 · ✓ 6 passed           │
//	│                               │
//	│ Dev stack  ▸ here             │
//	│  web    ● :3000               │
//	...
type Side struct {
	width, height int
	instance      *session.Instance
	stack         dev.Status
	// paneRate is how often the live pane is being sampled, per second.
	paneRate float64
	// notices are the session's recent notices, newest first.
	notices []Notice
	now     func() time.Time
}

// A Notice is one line the notice row showed for this session, and when.
type Notice struct {
	Text string
	At   time.Time
}

var sideTitleStyle, sideTextStyle, sideMutedStyle, sideRuleStyle lipgloss.Style

func init() { theme.OnChange(applySideTheme) }

func applySideTheme() {
	sideTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Muted))
	sideTextStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	sideMutedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	sideRuleStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
}

func NewSide() *Side { return &Side{now: time.Now} }

func (s *Side) SetSize(width, height int)              { s.width, s.height = width, height }
func (s *Side) SetInstance(instance *session.Instance) { s.instance = instance }
func (s *Side) SetDevStack(status dev.Status)          { s.stack = status }
func (s *Side) SetPaneRate(perSecond float64)          { s.paneRate = perSecond }
func (s *Side) SetNotices(notices []Notice)            { s.notices = notices }

func (s *Side) String() string {
	if s.width <= 0 || s.height <= 0 {
		return ""
	}
	title := ""
	if s.instance != nil {
		title = s.instance.Title
	}
	return frame(s.width, s.height, title, "", strings.Join(s.lines(), "\n"), sideRuleStyle, listLabelStyle)
}

// lines is the column's body: a heading per section, its facts indented
// under it, and a blank row between sections. Nothing is said about a fact
// that is not known.
func (s *Side) lines() []string {
	if s.instance == nil {
		return []string{sideMutedStyle.Render(" no session selected")}
	}
	i := s.instance
	heading := func(text, mark string) string {
		if mark != "" {
			return sideTitleStyle.Render(" "+text) + "  " + mark
		}
		return sideTitleStyle.Render(" " + text)
	}
	fact := func(text string) string { return "  " + text }
	var out []string
	section := func(lines ...string) {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}

	// Branch.
	var branch []string
	if i.NoWorktree() {
		branch = append(branch, heading("Directory", ""), fact(sideTextStyle.Render(repoIcon+" "+ShortenHome(i.Path))))
	} else {
		branch = append(branch, heading("Branch", ""))
		if b := i.DisplayBranch(); b != "" {
			branch = append(branch, fact(sideTextStyle.Render(branchIcon+" "+b)))
		}
		up := i.GetUpstreamStatus()
		switch {
		case up.Diverged():
			branch = append(branch, fact(sideMutedStyle.Render(up.Ref+" · ")+removedLinesStyle.Render(fmt.Sprintf("↑%d ↓%d diverged", up.Ahead, up.Behind))))
		case up.Behind > 0:
			branch = append(branch, fact(sideMutedStyle.Render(up.Ref+" · ")+ciPendingStyle.Render(fmt.Sprintf("↓%d behind", up.Behind))))
		case up.Ahead > 0:
			branch = append(branch, fact(sideMutedStyle.Render(up.Ref+" · ")+addedLinesStyle.Render(fmt.Sprintf("↑%d to push", up.Ahead))))
		case up.Tracked():
			branch = append(branch, fact(sideMutedStyle.Render(up.Ref+" · level")))
		case !up.FetchedAt.IsZero():
			branch = append(branch, fact(sideMutedStyle.Render("not pushed yet")))
		}
		if st := i.GetDiffStats(); st != nil && st.Error == nil && !st.IsEmpty() {
			branch = append(branch, fact(addedLinesStyle.Render("+"+compactCount(st.Added))+" "+removedLinesStyle.Render("−"+compactCount(st.Removed))+sideMutedStyle.Render(" against its base")))
		}
		if wt := i.GetWorktreePath(); wt != "" {
			branch = append(branch, fact(sideMutedStyle.Render(ShortenHome(wt))))
		}
	}
	section(branch...)

	// Pull request.
	st := i.GetCIStatus()
	if st.State != ci.StateNoPR && st.PRNumber > 0 {
		pr := []string{heading("Pull request", "")}
		plain, styled := ciBadgeAt(st, lipgloss.NoColor{}, badgeWordsAll)
		if plain != "" {
			pr = append(pr, fact(sideTextStyle.Render(fmt.Sprintf("#%d", st.PRNumber))+" · "+styled))
		}
		if st.Passed+st.Failed+st.Pending > 0 {
			pr = append(pr, fact(sideMutedStyle.Render(fmt.Sprintf("checks: %d passed, %d failed, %d running", st.Passed, st.Failed, st.Pending))))
		}
		section(pr...)
	}

	// Dev stack.
	if s.stack.Title != "" {
		mark := sideMutedStyle.Render("on " + s.stack.Title)
		if s.stack.Title == i.Title {
			mark = addedLinesStyle.Render(lampUpGlyph + " here")
		}
		stack := []string{heading("Dev stack", mark)}
		for _, l := range s.stack.Lamps {
			glyph, style := lampPendingGlyph, ciPendingStyle
			switch l.State {
			case dev.LampUp:
				glyph, style = lampUpGlyph, addedLinesStyle
			case dev.LampDown:
				glyph, style = lampDownGlyph, removedLinesStyle
			}
			line := sideTextStyle.Render(fmt.Sprintf("%-7s", l.Name)) + " " + style.Render(glyph)
			if l.Detail != "" {
				line += " " + sideMutedStyle.Render(l.Detail)
			}
			stack = append(stack, fact(line))
		}
		if s.stack.Phase != "" {
			stack = append(stack, fact(sideMutedStyle.Render(s.stack.Phase)))
		}
		section(stack...)
	}

	// Agent.
	agent := []string{heading("Agent", "")}
	state := "not started"
	switch {
	case i.Status == session.Paused:
		state = "checked out"
	case i.Status == session.Loading:
		state = "starting"
	case i.GetActivity() == session.ActivityNeedsInput:
		state = "waiting on your answer"
	case i.GetActivity() == session.ActivityWorking:
		state = "working"
	case i.GetActivity() == session.ActivityShell:
		state = "shell still running"
	case i.Started():
		state = "ready"
	}
	line := sideTextStyle.Render(i.Program) + sideMutedStyle.Render(" · "+state)
	if s.paneRate > 0 && i.Started() && i.Status != session.Paused {
		line += sideMutedStyle.Render(fmt.Sprintf(" · %g/s", s.paneRate))
	}
	agent = append(agent, fact(line))
	if at := i.LastActiveAt(); !at.IsZero() {
		if age := shortAge(s.now().Sub(at)); age != "" {
			agent = append(agent, fact(sideMutedStyle.Render("quiet for "+age)))
		}
	}
	section(agent...)

	// Notices.
	if len(s.notices) > 0 {
		notices := []string{heading("Notices", "")}
		for _, n := range s.notices {
			notices = append(notices, fact(sideMutedStyle.Render(n.At.Format("15:04")+" ")+sideTextStyle.Render(n.Text)))
		}
		section(notices...)
	}
	return out
}
