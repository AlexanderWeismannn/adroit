package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Strip is the one-line session name above the window in compact mode, where
// the list is behind the window and the row the window shows would otherwise
// be invisible:
//
//	‹ ● TASK-6664 signals tab strip  ⎇ TASK-6664  ▸ dev ›  2/4
//
// ‹ › are the keys that move between sessions without going back to the list.
func (l *List) Strip(width int) string {
	if width <= 0 {
		return ""
	}
	selected := l.GetSelectedInstance()
	if selected == nil {
		return clip(listLabelStyle.Render(" no session"), width)
	}
	count := fmt.Sprintf("%d/%d ", l.selectedIdx+1, len(l.items))
	left, right := " ‹", "› "
	// The row gets what is left between the arrows and the count.
	inner := width - len(left) - len(right) - lipgloss.Width(count)
	if inner < 8 {
		return clip(listLabelStyle.Render(left+right+count), width)
	}
	row := l.renderer.compactAt(selected, false, l.selectedIdx == len(l.items)-1, inner)
	line := listLabelStyle.Render(left) + row + listLabelStyle.Render(right+count)
	if w := lipgloss.Width(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	return clip(line, width)
}
