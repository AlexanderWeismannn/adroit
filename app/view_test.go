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

// Under an overlay the bar is empty: it kept advertising the main screen's
// keys -- ↵ attach, i prompt, D kill -- under a kill confirmation that answers
// none of them, and repeated the prompt overlay's own keys line.
func TestTheBarIsEmptyUnderAnOverlay(t *testing.T) {
	h := newSizedHome(t, 100, 30)
	bar := func() string {
		rows := strings.Split(ansi.Strip(h.view()), "\n")
		return rows[h.layout.Bar.Y]
	}
	require.Contains(t, bar(), "new", "the bar names the main screen's keys with no overlay open")
	h.confirmAction("Kill session", "Kill TASK-5700-session?", "kill", nil)
	require.Empty(t, strings.TrimSpace(bar()), "the main screen's keys are live under the dialog")
}

// The settings box keeps clear of the bar: at 24 rows its fixed sixteen-line
// body pushed it, keys line and all, over the bar.
func TestSettingsFitAShortTerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := newSizedHome(t, 70, 24)
	h.openSettings()
	rows := strings.Split(ansi.Strip(h.view()), "\n")
	for _, row := range rows[h.layout.Bar.Y:] {
		require.NotContains(t, row, "╯", "the settings box runs into the bar: %q", row)
		require.NotContains(t, row, "│", "the settings box runs into the bar: %q", row)
	}
}
