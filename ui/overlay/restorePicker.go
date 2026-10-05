package overlay

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RestoreEntry is one killed session offered back to the user.
//
// Plain strings rather than the stored record, so the picker stays a list of
// rows: what a restore actually does with the choice is the caller's business,
// and the overlay has no opinion about sessions, transcripts or git.
type RestoreEntry struct {
	// Title is what the session was called.
	Title string
	// Branch is the branch it was on, or empty if it had none.
	Branch string
	// Summary is the first thing the user said in that conversation.
	Summary string
	// KilledAt is when it was killed, rendered as an age.
	KilledAt time.Time
	// Resumable is whether a transcript was found for it. A row without one can
	// still be chosen -- it starts the same program on a fresh branch -- but it
	// says so, rather than quietly starting a conversation with no history.
	Resumable bool
}

// RestorePicker chooses a killed session to bring back.
type RestorePicker struct {
	entries []RestoreEntry
	cursor  int
	width   int
}

// NewRestorePicker opens on the most recently killed session, which is very
// nearly always the one that was killed by accident a moment ago.
func NewRestorePicker(entries []RestoreEntry) *RestorePicker {
	return &RestorePicker{entries: entries, width: 74}
}

// Selected is the entry under the cursor, and whether there is one at all.
func (r *RestorePicker) Selected() (RestoreEntry, bool) {
	if r.cursor < 0 || r.cursor >= len(r.entries) {
		return RestoreEntry{}, false
	}
	return r.entries[r.cursor], true
}

// SetWidth bounds the rendered box, so a long first message cannot push the
// overlay wider than the terminal.
func (r *RestorePicker) SetWidth(width int) {
	if width > 0 {
		r.width = width
	}
}

func (r *RestorePicker) HandleKeyPress(msg tea.KeyMsg) PickerAction {
	switch msg.Type {
	case tea.KeyUp:
		return r.move(-1)
	case tea.KeyDown:
		return r.move(1)
	case tea.KeyEnter:
		if len(r.entries) == 0 {
			return PickerCancel
		}
		return PickerConfirm
	case tea.KeyEsc:
		return PickerCancel
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "k":
			return r.move(-1)
		case "j":
			return r.move(1)
		case "q":
			return PickerCancel
		}
	}
	return PickerNone
}

func (r *RestorePicker) move(delta int) PickerAction {
	next := r.cursor + delta
	if next < 0 || next >= len(r.entries) {
		return PickerNone
	}
	r.cursor = next
	return PickerPreview
}

// contentWidth is how much room a row actually has: the width given to the box
// is what lipgloss fills, and the padding it adds comes out of it.
func (r *RestorePicker) contentWidth() int {
	return r.width - 4
}

// maxRestoreRows is how many sessions are shown at once; the list windows
// around the cursor past that.
const maxRestoreRows = 8

func (r *RestorePicker) Render() string {
	var body strings.Builder
	body.WriteString(restoreTitleStyle.Render("Restore a killed session"))
	body.WriteString(restoreHintStyle.Render("   ↑↓ select · enter restore · esc cancel"))
	body.WriteString("\n\n")

	if len(r.entries) == 0 {
		body.WriteString(restoreHintStyle.Render("  Nothing to restore — no session has been killed yet."))
		return restoreBorderStyle.Render(body.String())
	}

	start := 0
	if r.cursor >= maxRestoreRows {
		start = r.cursor - maxRestoreRows + 1
	}
	end := start + maxRestoreRows
	if end > len(r.entries) {
		end = len(r.entries)
	}

	// The label column is sized to the widest title actually on screen, so the
	// ages and messages line up without a fixed width that a short list would
	// only pad out.
	labelWidth := 0
	for i := start; i < end; i++ {
		if width := lipgloss.Width(r.entries[i].Title); width > labelWidth {
			labelWidth = width
		}
	}

	for i := start; i < end; i++ {
		entry := r.entries[i]
		marker, label := "  ", restoreRowStyle
		if i == r.cursor {
			marker, label = restoreMarkerStyle.Render("▸ "), restoreSelectedStyle
		}

		body.WriteString(marker)
		body.WriteString(label.Render(padRight(entry.Title, labelWidth)))
		// Two columns of border and two of padding on each side are outside the
		// width the box is given, so the text has to fit what is left. A row that
		// does not is wrapped by lipgloss onto an unindented continuation line,
		// which reads as a row of its own.
		body.WriteString(restoreMetaStyle.Render(truncateTo("  "+entry.describe(), r.contentWidth()-2-labelWidth)))
		body.WriteString("\n")

		detail := entry.Summary
		if detail == "" {
			detail = "(no message recorded)"
		}
		if !entry.Resumable {
			detail = "no transcript found — starts fresh"
		}
		body.WriteString("    ")
		body.WriteString(restoreDetailStyle.Render(truncateTo(detail, r.contentWidth()-4)))
		if i != end-1 {
			body.WriteString("\n")
		}
	}

	if len(r.entries) > end-start {
		body.WriteString("\n")
		body.WriteString(restoreHintStyle.Render(
			fmt.Sprintf("  %d more below", len(r.entries)-end)))
	}

	return restoreBorderStyle.Width(r.width).Render(body.String())
}

// describe is the right-hand metadata of a row: how long ago it was killed, and
// the branch it was on when it was.
func (e RestoreEntry) describe() string {
	parts := []string{killedAgo(e.KilledAt)}
	if e.Branch != "" {
		parts = append(parts, e.Branch)
	}
	return strings.Join(parts, " · ")
}

// killedAgo renders an age the way the session list does: coarse, and never
// longer than it needs to be.
func killedAgo(at time.Time) string {
	if at.IsZero() {
		return "killed"
	}
	elapsed := time.Since(at)
	switch {
	case elapsed < time.Minute:
		return "killed just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("killed %dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("killed %dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("killed %dd ago", int(elapsed.Hours())/24)
	}
}

func truncateTo(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

var (
	restoreBorderStyle   lipgloss.Style
	restoreTitleStyle    lipgloss.Style
	restoreHintStyle     lipgloss.Style
	restoreRowStyle      lipgloss.Style
	restoreSelectedStyle lipgloss.Style
	restoreMarkerStyle   lipgloss.Style
	restoreMetaStyle     lipgloss.Style
	restoreDetailStyle   lipgloss.Style
)

func init() { theme.OnChange(applyRestorePickerTheme) }

func applyRestorePickerTheme() {
	restoreBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Color(theme.Accent)).
		Padding(1, 2)
	restoreTitleStyle = lipgloss.NewStyle().
		Foreground(theme.Color(theme.Accent)).
		Bold(true)
	restoreHintStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	restoreRowStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	restoreSelectedStyle = lipgloss.NewStyle().
		Foreground(theme.Color(theme.Accent)).
		Bold(true)
	restoreMarkerStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
	restoreMetaStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	restoreDetailStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
}
