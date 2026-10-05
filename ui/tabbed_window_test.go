package ui

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"

	"github.com/stretchr/testify/require"
)

func newTestTabbedWindow() *TabbedWindow {
	return NewTabbedWindow(NewPreviewPane(), NewDiffPane(), NewTerminalPane(), NewRunPane(dev.New(nil)))
}

// The tab constants and the label slice are two parallel lists. They select
// different things -- the constant picks which pane String() renders, the slice
// picks which label is highlighted -- so a tab added or reordered in one and not
// the other highlights one tab while showing another, with nothing to catch it.
func TestTabConstantsIndexTheLabelsTheyName(t *testing.T) {
	w := newTestTabbedWindow()

	for _, tt := range []struct {
		index int
		label string
	}{
		{PreviewTab, "Preview"},
		{DiffTab, "Diff"},
		{TerminalTab, "Terminal"},
		{RunTab, "Run"},
	} {
		require.Less(t, tt.index, len(w.tabs), "%s is out of range of the labels", tt.label)
		require.Equal(t, tt.label, w.tabs[tt.index])
	}
	require.Len(t, w.tabs, 4, "a new tab needs a constant and a label, in the same order")
}

// Selecting the run tab must be what IsInRunTab reports and what Toggle cycles
// through, or the key that opens it and the key that attaches to it disagree.
func TestSelectRunTabIsTheTabTheWindowReports(t *testing.T) {
	w := newTestTabbedWindow()
	require.False(t, w.IsInRunTab())

	w.SelectRunTab()
	require.True(t, w.IsInRunTab())
	require.Equal(t, RunTab, w.GetActiveTab())

	w.SetSize(120, 40)
	require.Contains(t, w.String(), "Run", "the run tab must be drawn in the tab bar")
}

// Toggle has to reach the run tab, or the only way in is the key that starts a
// stack -- and looking at one you did not just start would be impossible.
func TestToggleReachesEveryTab(t *testing.T) {
	w := newTestTabbedWindow()
	seen := map[int]bool{w.GetActiveTab(): true}
	for i := 0; i < len(w.tabs); i++ {
		w.Toggle()
		seen[w.GetActiveTab()] = true
	}
	require.Len(t, seen, len(w.tabs), "Toggle did not visit every tab")
	require.True(t, seen[RunTab])
}

// The label is what the user looks for; a rename that missed it would leave the
// panel introducing itself by its old name.
func TestTabBarNamesTheRunTabNotTheOldOne(t *testing.T) {
	w := newTestTabbedWindow()
	w.SetSize(120, 40)
	bar := w.String()
	require.Contains(t, bar, "Run")
	require.False(t, strings.Contains(bar, "Dev"), "the tab bar still names the old Dev panel")
}

func devTabInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: t.TempDir(), Status: session.Paused, Program: "claude", NoWorktree: true,
	})
	require.NoError(t, err)
	return inst
}

// The question "is the stack running, and is it running for THIS session" gets
// asked from whichever tab you are on. The list badge only marks the row that
// owns it, and the Run pane only says so once you have switched to it -- so the
// tab itself carries the verdict.
func TestTheRunTabCarriesTheStackState(t *testing.T) {
	here := devTabInstance(t, "TASK-5719")
	elsewhere := devTabInstance(t, "post-ai-svg")

	for _, c := range []struct {
		name     string
		selected *session.Instance
		title    string
		state    dev.LampState
		want     string
	}{
		{"nothing running", here, "", dev.LampDown, "Run"},
		{"running here and ready", here, "TASK-5719", dev.LampUp, "Run " + lampUpGlyph},
		{"running here, coming up", here, "TASK-5719", dev.LampPending, "Run " + lampPendingGlyph},
		{"running here, a lamp down", here, "TASK-5719", dev.LampDown, "Run " + lampDownGlyph},
		// The distinction that matters most: the stack is up, but not on this
		// session -- so pressing d would move it, not start it.
		{"running on another session", elsewhere, "TASK-5719", dev.LampUp, "Run " + devStackIcon},
		{"no session selected", nil, "TASK-5719", dev.LampUp, "Run " + devStackIcon},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newTestTabbedWindow()
			w.SetSize(80, 20)
			w.SetInstance(c.selected)
			w.SetDevStack(c.title, c.state)
			require.Equal(t, c.want, plain(w.runTabLabel(16)))
		})
	}
}

// A tab too narrow for the suffix would wrap it onto a second row and take the
// whole bar down with it, so the label gives way rather than the layout.
func TestTheRunTabDropsItsIndicatorRatherThanWrapping(t *testing.T) {
	w := newTestTabbedWindow()
	w.SetInstance(devTabInstance(t, "TASK-5719"))
	w.SetDevStack("TASK-5719", dev.LampUp)

	require.Equal(t, "Run", plain(w.runTabLabel(4)), "no room for the suffix")
	require.Equal(t, "Run "+lampUpGlyph, plain(w.runTabLabel(5)), "just enough room")
}

// And the whole bar still renders one row per tab at a realistic width.
func TestTheTabBarStaysOneRowWithTheIndicator(t *testing.T) {
	w := newTestTabbedWindow()
	w.SetSize(80, 20)
	w.SetInstance(devTabInstance(t, "TASK-5719"))
	w.SetDevStack("TASK-5719", dev.LampUp)

	var bar string
	for _, row := range strings.Split(w.String(), "\n") {
		if strings.Contains(plain(row), "Run") {
			bar = plain(row)
			break
		}
	}
	require.Contains(t, bar, "Preview")
	require.Contains(t, bar, "Run "+lampUpGlyph)
	require.NotContains(t, bar, "\n")
}

// A label wider than its tab wraps onto a second row, and one two-row tab takes
// the whole bar with it: at 60 columns "Terminal" broke in half and the tabs
// either side of it came apart. The name gives way rather than the layout.
func TestANarrowTabGivesUpItsLabelRatherThanTheBar(t *testing.T) {
	w := newTestTabbedWindow()
	w.SetSize(42, 20)

	rows := strings.Split(w.String(), "\n")
	labelRow := -1
	for i, row := range rows {
		if strings.Contains(plain(row), "Diff") {
			labelRow = i
			break
		}
	}
	require.NotEqual(t, -1, labelRow, "the bar should have a row of labels")
	require.Contains(t, plain(rows[labelRow]), "Run", "every label sits on that one row")
	require.Equal(t, 42, lipgloss.Width(rows[labelRow]), "the bar is exactly as wide as the window")
	require.True(t, strings.HasPrefix(plain(rows[labelRow]), "╭") && strings.HasSuffix(plain(rows[labelRow]), "╮"),
		"and is the window's own top border")
	// A wrapped label leaves its tail on that closing row -- "Termina" above and
	// a lone "l" below, with the bar broken around it.
	require.NotRegexp(t, `[A-Za-z]`, plain(rows[labelRow+1]), "no label spills onto the row below")
	require.NotContains(t, plain(w.String()), "Terminal", "the label that does not fit is elided")
}

// The window hands its panes what is left after its own chrome, and at a height
// smaller than the chrome that used to be negative. No pane can draw a negative
// size; every one of them is given zero instead.
func TestTheWindowNeverHandsAPaneANegativeSize(t *testing.T) {
	stack := dev.New(nil)
	for _, w := range []int{0, 1, 5, 30} {
		for _, h := range []int{0, 1, 2, 3, 4} {
			win := NewTabbedWindow(NewPreviewPane(), NewDiffPane(), NewTerminalPane(), NewRunPane(stack))
			win.SetSize(w, h)
			pw, ph := win.GetPreviewSize()
			require.GreaterOrEqual(t, pw, 0, "%dx%d width", w, h)
			require.GreaterOrEqual(t, ph, 0, "%dx%d height", w, h)
			for tab := 0; tab < 4; tab++ {
				require.NotPanics(t, func() { _ = win.String() }, "%dx%d tab %d", w, h, tab)
				win.Toggle()
			}
		}
	}
}
