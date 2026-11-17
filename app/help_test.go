package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// The general help card is taller than most terminals. It used to be drawn at
// its natural size and cut by the screen, so its last section and its keys line
// were never visible at any size from 70x24 to 200x40. It now fits the screen,
// keeps its keys line, and scrolls to its last key.
func TestHelpCardFitsAndScrollsToItsLastKey(t *testing.T) {
	for _, size := range [][2]int{{70, 24}, {100, 30}, {140, 34}} {
		h := newSizedHome(t, size[0], size[1])
		h.appState = &fakeAppState{}
		h.showHelpScreen(helpTypeGeneral{}, nil)
		view := ansi.Strip(h.view())
		for i, line := range strings.Split(view, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), size[0], "%dx%d row %d overruns: %q", size[0], size[1], i, line)
		}
		require.Contains(t, view, "close", "%dx%d: the card's keys line is off the screen", size[0], size[1])

		_ = h.overlay.handleKey(h, tea.KeyPressMsg{Code: tea.KeyEnd})
		require.NotNil(t, h.overlay, "%dx%d: a scroll key closed the card", size[0], size[1])
		require.Contains(t, ansi.Strip(h.view()), "Quit the application", "%dx%d: the last key is unreachable", size[0], size[1])
	}
}
