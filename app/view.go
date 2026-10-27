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
	if m.state == statePrompt {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true))
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true))
	} else if m.state == stateSettings && m.settings != nil {
		m.settings.SetWidth(m.termWidth)
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.settings.Render(), mainView, true, true))
	} else if m.state == stateTheme {
		if m.themePicker == nil {
			log.ErrorLog.Printf("theme picker is nil")
			return mainView
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.themePicker.Render(), mainView, true, true))
	} else if m.state == stateRestore {
		if m.restorePicker == nil {
			log.ErrorLog.Printf("restore picker is nil")
			return mainView
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.restorePicker.Render(), mainView, true, true))
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true))
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
	for i, line := range lines {
		if lipgloss.Width(line) > m.termWidth {
			lines[i] = truncate.String(line, uint(m.termWidth))
		}
	}
	return strings.Join(lines, "\n")
}
