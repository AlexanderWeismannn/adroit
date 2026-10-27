package app

import (
	"strings"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
)

func (m *home) View() string {
	// Each pane leads with one blank row of its own, which is all the air the top
	// of the screen needs; a padding row here used to add a second.
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, m.list.String(), m.tabbedWindow.String())

	// Left, not centred: the menu spans the terminal and the panes stop just
	// short of it, so centring shifted the whole frame right by the difference
	// and left an unused strip on each side.
	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)
	mainView = m.fitToTerminal(mainView)

	// Overlays are fitted too, and after being placed: an overlay is sized off
	// the terminal, but the confirmation is a fixed 50 columns and a narrow
	// terminal is exactly where it does not fit.
	if m.overlay != nil {
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.overlay.render(m), mainView, true, true))
	}

	return mainView
}

// fitToTerminal holds the composed frame to the width of the screen.
//
// The panes budget themselves and the list and window each keep to the column
// they were given, so this changes nothing at a usable size. It is the backstop
// for the sizes where the budget cannot be met at all -- four bordered tabs need
// eight columns before a single label, so below about twenty the frame cannot be
// drawn to fit. Cut, the interface loses its right-hand edge; uncut, tmux cuts it
// anyway and the rows that overran take the layout with them.
func (m *home) fitToTerminal(view string) string {
	if m.termWidth <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	cut := false
	for i, line := range lines {
		if lipgloss.Width(line) > m.termWidth {
			lines[i] = truncate.String(line, uint(m.termWidth))
			cut = true
		}
	}
	// Every component is given a rect and is expected to keep to it, so a cut
	// here is a component overrunning its rect, and worth knowing about. Once
	// per width: a terminal too narrow for the frame at all would otherwise log
	// on every frame.
	if cut && m.fitWarnedWidth != m.termWidth {
		m.fitWarnedWidth = m.termWidth
		log.WarningLog.Printf("a row overran the terminal at %d columns and was cut", m.termWidth)
	}
	return strings.Join(lines, "\n")
}
