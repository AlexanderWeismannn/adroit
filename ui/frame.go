package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// frame draws a rounded box exactly width x height, with title set into the top
// border and foot into the bottom one, and body's lines inside:
//
//	╭─ Sessions ───────────╮
//	│ ...                  │
//	╰─ 2/4 ────────────────╯
//
// A body shorter than the box is padded, a longer one cut, and every line is
// held to the inner width: the box is a fixed-size pane and must occupy exactly
// the cells it was given, or the frame composed from it is wrong by that much.
// title and foot are plain text; a label too long for the rule is dropped.
func frame(width, height int, title, foot string, body string, rule, label lipgloss.Style) string {
	if width < 2 || height < 2 {
		return lipgloss.Place(max(width, 0), max(height, 0), lipgloss.Left, lipgloss.Top, "")
	}
	inner := width - 2
	edge := func(left, right, text string, style lipgloss.Style) string {
		// "╭─ text ─…─╮": the corners, one rule cell, then the label.
		fill := width - 3 - lipgloss.Width(text)
		if text != "" && fill >= 0 {
			return rule.Render(left+"─") + style.Render(text) + rule.Render(strings.Repeat("─", fill)+right)
		}
		return rule.Render(left + strings.Repeat("─", inner) + right)
	}
	var lines []string
	if body != "" {
		lines = strings.Split(body, "\n")
	}
	rows := make([]string, 0, height)
	rows = append(rows, edge("╭", "╮", pad(title), label))
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		line = clip(line, inner)
		if w := lipgloss.Width(line); w < inner {
			line += strings.Repeat(" ", inner-w)
		}
		rows = append(rows, rule.Render("│")+line+rule.Render("│"))
	}
	rows = append(rows, edge("╰", "╯", pad(foot), label))
	return strings.Join(rows, "\n")
}

// pad gives a border label the space on each side that sets it off the rule.
func pad(text string) string {
	if text == "" {
		return ""
	}
	return " " + text + " "
}
