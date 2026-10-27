package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickChangesModeAtTheBreakpoints(t *testing.T) {
	for width, want := range map[int]Mode{0: Compact, 79: Compact, 89: Compact, 90: Split, 159: Split, 160: Wide, 200: Wide} {
		require.Equal(t, want, Pick(width), "%d columns", width)
	}
}

// Every mode at every size: the rows add up to the height, the columns to the
// width, and no rect reaches past the terminal or below zero.
func TestLayoutFillsTheTerminalExactly(t *testing.T) {
	within := func(t *testing.T, name string, r Rect, width, height int) {
		t.Helper()
		require.GreaterOrEqual(t, r.W, 0, "%s width", name)
		require.GreaterOrEqual(t, r.H, 0, "%s height", name)
		require.LessOrEqual(t, r.X+r.W, width, "%s runs past the right edge", name)
		require.LessOrEqual(t, r.Y+r.H, height, "%s runs past the bottom", name)
	}
	for _, mode := range []Mode{Compact, Split, Wide} {
		for _, width := range []int{0, 1, 2, 20, 79, 80, 89, 90, 110, 159, 160, 200} {
			for _, height := range []int{0, 1, 2, 3, 20, 24, 30, 50} {
				r := Layout(width, height, mode)
				for name, rect := range map[string]Rect{"header": r.Header, "list": r.List, "window": r.Window,
					"strip": r.Strip, "side": r.Side, "bar": r.Bar, "notice": r.Notice} {
					within(t, name, rect, width, height)
				}
				require.Equal(t, height, r.Header.H+r.List.H+r.Bar.H+r.Notice.H,
					"%s %dx%d: the rows do not add up", mode, width, height)
				switch mode {
				case Split:
					require.Equal(t, width, r.List.W+r.Window.W, "%s %dx%d: the columns do not add up", mode, width, height)
				case Wide:
					require.Equal(t, width, r.List.W+r.Window.W+r.Side.W, "%s %dx%d: the columns do not add up", mode, width, height)
				case Compact:
					require.Equal(t, r.List.H, r.Strip.H+r.Window.H, "%s %dx%d: the strip and the window share the list's rows", mode, width, height)
				}
			}
		}
	}
}

// The split geometry: a header row, the list at three tenths of the width, and
// the last two rows for the bar and the notice.
func TestSplitKeepsTheEstablishedGeometry(t *testing.T) {
	r := Layout(110, 30, Split)
	require.Equal(t, Rect{0, 0, 110, 1}, r.Header)
	require.Equal(t, Rect{0, 1, 33, 27}, r.List)
	require.Equal(t, Rect{33, 1, 77, 27}, r.Window)
	require.Equal(t, Rect{0, 28, 110, 1}, r.Bar)
	require.Equal(t, Rect{0, 29, 99, 1}, r.Notice)
}
