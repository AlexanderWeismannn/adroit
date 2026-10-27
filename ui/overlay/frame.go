package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/charmbracelet/x/ansi"
)

// Frame is the one chrome every overlay wears: the title set into the top
// border, the body with a row of air above and below and two cells each side,
// a rule, and under it the keys that work here.
//
//	╭─ Kill session ──────────────────────────╮
//	│                                          │
//	│  Kill TASK-6571 ews attribution?         │
//	│                                          │
//	├──────────────────────────────────────────┤
//	│  y kill   esc keep                       │
//	╰──────────────────────────────────────────╯
//
// width is the outer width. A body line wider than the inside is cut; the
// footer is one line, already styled (see Keys). border colours the rule.
func Frame(width int, title, body, footer string, border color.Color) string {
	if width < 4 {
		width = 4
	}
	inner := width - 2
	rule := lipgloss.NewStyle().Foreground(border)
	label := lipgloss.NewStyle().Foreground(theme.Color(theme.Text)).Bold(true)

	top := rule.Render("╭─")
	used := 2
	if title != "" {
		t := " " + title + " "
		if lipgloss.Width(t) > inner-2 {
			t = ansi.Truncate(t, max(inner-2, 1), "…")
		}
		top += label.Render(t)
		used += lipgloss.Width(t)
	}
	top += rule.Render(strings.Repeat("─", max(width-used-1, 0)) + "╮")

	row := func(text string) string {
		text = "  " + text
		if w := lipgloss.Width(text); w > inner {
			text = ansi.Truncate(text, inner, "")
		} else if w < inner {
			text += strings.Repeat(" ", inner-w)
		}
		return rule.Render("│") + text + rule.Render("│")
	}
	rows := []string{top, row("")}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		rows = append(rows, row(line))
	}
	rows = append(rows, row(""))
	if footer != "" {
		rows = append(rows, rule.Render("├"+strings.Repeat("─", inner)+"┤"), row(footer))
	}
	rows = append(rows, rule.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(rows, "\n")
}

// A Key is one entry of a frame's footer: the key and what it does here.
type Key struct {
	Key, Label string
}

// Keys styles a footer: keys in the accent, words muted, three cells apart.
func Keys(keys ...Key) string {
	key := lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent))
	word := lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, key.Render(k.Key)+" "+word.Render(k.Label))
	}
	return strings.Join(parts, "   ")
}
