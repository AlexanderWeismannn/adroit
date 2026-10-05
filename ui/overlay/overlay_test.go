package overlay

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An overlay taller than the screen used to be centred, which put its origin
// ABOVE the top: the rows that fell off were its first ones. Losing the tail of
// a help screen is recoverable by making the window taller; losing its title
// just looks broken.
func TestAnOverlayTallerThanTheScreenKeepsItsTop(t *testing.T) {
	bg := strings.Repeat("background\n", 10)
	fg := "TITLE\nsecond\nthird\n" + strings.Repeat("filler\n", 20)

	out := PlaceOverlay(0, 0, strings.TrimRight(fg, "\n"), strings.TrimRight(bg, "\n"), false, true)
	lines := strings.Split(out, "\n")

	require.Contains(t, lines[0], "TITLE", "the overlay's first line must be the screen's first line")
	require.Contains(t, lines[1], "second")
	require.Len(t, lines, 10, "the composed view must still be the height of the screen")
}

// The ordinary case must keep centring.
func TestAnOverlayThatFitsIsStillCentred(t *testing.T) {
	bg := strings.TrimRight(strings.Repeat("background\n", 11), "\n")
	fg := "TITLE\nsecond\nthird"

	lines := strings.Split(PlaceOverlay(0, 0, fg, bg, false, true), "\n")
	require.Len(t, lines, 11)
	require.Contains(t, lines[4], "TITLE", "an overlay that fits should sit in the middle")
	require.Contains(t, lines[0], "background")
}
