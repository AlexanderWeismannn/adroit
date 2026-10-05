package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/session/dev"

	"github.com/mattn/go-runewidth"
	"github.com/stretchr/testify/require"
)

var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// lampStates walks a stack from launch to ready, plus the failure the ownership
// check reports -- the states a header actually cycles through while watched.
func lampStates() map[string][]dev.Lamp {
	return map[string][]dev.Lamp{
		"launching": {
			{Name: "redis", State: dev.LampPending, Detail: "waiting"},
			{Name: "web", State: dev.LampPending, Detail: "waiting"},
			{Name: "worker", State: dev.LampPending, Detail: "waiting"},
			{Name: "client", State: dev.LampPending, Detail: "not listening"},
		},
		"coming up": {
			{Name: "redis", State: dev.LampUp, Detail: "up"},
			{Name: "web", State: dev.LampPending, Detail: "no response"},
			{Name: "worker", State: dev.LampPending, Detail: "no response"},
			{Name: "client", State: dev.LampUp, Detail: "up"},
		},
		"a port held elsewhere": {
			{Name: "redis", State: dev.LampUp, Detail: "up"},
			{Name: "web", State: dev.LampDown, Detail: "held by another worktree: .../worktrees/TASK-5719-Entity-Matching-Hydration_18d1326a4b37f73c"},
			{Name: "worker", State: dev.LampPending, Detail: "no response"},
			{Name: "client", State: dev.LampUp, Detail: "up"},
		},
		"ready": {
			{Name: "redis", State: dev.LampUp, Detail: "up"},
			{Name: "web", State: dev.LampUp, Detail: "up"},
			{Name: "worker", State: dev.LampUp, Detail: "up"},
			{Name: "client", State: dev.LampUp, Detail: "up"},
		},
	}
}

func headerFor(width int, lamps []dev.Lamp, phase string) []string {
	p := NewRunPane(dev.New(nil))
	p.width, p.height = width, 20
	p.status = dev.Status{Running: true, Title: "TASK-5632-Audience-Registry", Lamps: lamps, Phase: phase}
	return strings.Split(p.header(), "\n")
}

// The bug this pins: the lamps used to share one line, so a detail changing
// length -- "waiting" to "no response" to "up", every poll -- shifted every lamp
// to its right. The icons moved continuously through the exact interval you are
// watching them, which is unreadable. Nothing before the detail may move.
func TestLampGlyphsAndNamesHoldTheirColumns(t *testing.T) {
	var reference []string

	for name, lamps := range lampStates() {
		lines := headerFor(78, lamps, "waiting for the stack to come up")
		require.Len(t, lines, 6, "%s: title + 4 lamps + rule", name)

		// Everything up to the detail is the fixed part of the row: two spaces,
		// the glyph, a space, and the name padded to a shared width.
		prefixes := make([]string, 0, 4)
		for _, line := range lines[1:5] {
			row := []rune(plain(line))
			require.Greater(t, len(row), 10, "%s: lamp row is too short to hold a name", name)
			// The glyph varies by state; the columns either side of it must not.
			prefixes = append(prefixes, string(row[:2])+"?"+string(row[3:11]))
		}

		if reference == nil {
			reference = prefixes
			continue
		}
		require.Equal(t, reference, prefixes,
			"%s: the lamp column moved; glyph and name positions must not depend on state", name)
	}
}

// A detail long enough to wrap would make the header a row taller and shift
// every line of output below it -- the same jump, by another route.
func TestALongDetailIsTruncatedRatherThanWrapped(t *testing.T) {
	const width = 78
	lines := headerFor(width, lampStates()["a port held elsewhere"], "waiting")

	require.Len(t, lines, 6, "a long detail must not add rows")
	for i, line := range lines {
		require.LessOrEqual(t, runewidth.StringWidth(plain(line)), width,
			"line %d overruns the pane and would wrap: %q", i, plain(line))
	}
	require.Contains(t, plain(lines[2]), "…", "the overlong detail should be marked as cut")
}

// The phase is the part of the title line that changes, so it goes last: a
// segment after it would be pushed sideways every time it changed.
func TestTheTitleLineKeepsItsIdentityFixed(t *testing.T) {
	const width = 78
	lamps := lampStates()["ready"]

	var prefix string
	for _, phase := range []string{"launching the stack", "waiting for the stack to come up", "running", ""} {
		title := plain(headerFor(width, lamps, phase)[0])
		require.True(t, strings.HasPrefix(title, "stack › TASK-5632-Audience-Registry"),
			"the stack's identity must lead the line, got %q", title)
		if prefix == "" {
			prefix = title[:len("stack › TASK-5632-Audience-Registry")]
			continue
		}
		require.Equal(t, prefix, title[:len(prefix)])
	}
}

// The header's height is what the output below is positioned from, so it must
// not depend on how the stack is doing.
func TestHeaderHeightDoesNotDependOnState(t *testing.T) {
	for name, lamps := range lampStates() {
		require.Len(t, headerFor(78, lamps, "running"), headerLines(len(lamps)), name)
	}
	// And an adopted stack seeds its lamps, so this never starts smaller.
	require.Equal(t, 6, headerLines(4))
	require.Equal(t, 3, headerLines(0), "the no-checks line still occupies a row")
}

// A narrow pane must still produce one line per lamp, not wrapped fragments.
func TestANarrowPaneStillRendersOneRowPerLamp(t *testing.T) {
	for _, width := range []int{12, 24, 40} {
		lines := headerFor(width, lampStates()["coming up"], "waiting for the stack to come up")
		require.Len(t, lines, 6, "width %d", width)
		for i, line := range lines {
			require.LessOrEqual(t, runewidth.StringWidth(plain(line)), width, "width %d, line %d", width, i)
		}
	}
}

// configuredRunPane builds a pane whose stack is configured, so String() renders
// the header rather than the "nothing configured" splash.
func configuredRunPane(t *testing.T, width, height int) *RunPane {
	t.Helper()
	p := NewRunPane(dev.New(&config.DevConfig{Command: "true", Checks: []config.DevCheck{
		{Name: "redis"}, {Name: "web"}, {Name: "worker"}, {Name: "client"},
	}}))
	p.width, p.height = width, height
	p.status = dev.Status{
		Running: true,
		Title:   "TASK-5632-Audience-Registry",
		Phase:   "waiting for the stack to come up",
		Lamps:   lampStates()["coming up"],
	}
	return p
}

// The pane must render exactly as many rows as it was given.
//
// The bug: tmux hands back joined lines -- capture-pane -J undoes tmux's own
// wrapping -- so one log line can be far wider than the pane. Rendered into a
// fixed-width block those wrapped again, turning an 8-row body of long lines
// into 16 rows. The block overflowed its box and the lamps were pushed out of
// view, which is precisely when a stack is starting and the lines are longest.
func TestLongOutputDoesNotPushTheLampsOutOfView(t *testing.T) {
	const width, height = 60, 14
	p := configuredRunPane(t, width, height)

	long := "[worker] 2026-09-01 15:21:44 [info] 742afbf4-75d8-4a7a-9099-89fcff97d3a5:worker " +
		"Ensuring Redis connection and warming the employer title authority cache"
	lines := make([]string, 8)
	for i := range lines {
		lines[i] = long
	}
	p.content = strings.Join(lines, "\n")

	rows := strings.Split(p.String(), "\n")
	require.Len(t, rows, height, "the pane rendered %d rows into a %d-row box", len(rows), height)

	// And the lamps are still where they belong: the first rows, in order.
	require.Contains(t, plain(rows[0]), "stack › TASK-5632-Audience-Registry")
	for i, name := range []string{"redis", "web", "worker", "client"} {
		require.Contains(t, plain(rows[1+i]), name, "row %d should be the %s lamp", 1+i, name)
	}
}

// Every row must fit, or the one that does not wraps and takes a row from
// something else.
func TestNoRenderedRowOverrunsThePane(t *testing.T) {
	for _, width := range []int{40, 60, 100} {
		p := configuredRunPane(t, width, 14)
		p.content = strings.Join([]string{
			strings.Repeat("x", 400),
			"\x1b[32m[web]\x1b[0m " + strings.Repeat("coloured output ", 30),
			"short",
		}, "\n")

		for i, row := range strings.Split(p.String(), "\n") {
			require.LessOrEqual(t, runewidth.StringWidth(plain(row)), width,
				"width %d, row %d overruns: %q", width, i, plain(row))
		}
	}
}

// Cutting a line mid-way drops whatever reset closed it, so without one of our
// own the last colour bleeds down the rest of the pane.
func TestAClippedLineIsClosedOff(t *testing.T) {
	got := clip("\x1b[31m"+strings.Repeat("red ", 40), 20)
	require.True(t, strings.HasSuffix(got, "\x1b[0m"), "a clipped line must end reset, got %q", got)
	require.LessOrEqual(t, runewidth.StringWidth(plain(got)), 20)
	require.Contains(t, plain(got), "…", "a clipped line should show it was cut")
}

// A line that already fits is passed through untouched -- no stray ellipsis, no
// reset appended to output that closed itself properly.
func TestAFittingLineIsLeftAlone(t *testing.T) {
	line := "\x1b[32m[web] listening\x1b[0m"
	require.Equal(t, line, clip(line, 60))
	require.Equal(t, line, clip(line, runewidth.StringWidth(plain(line))))
}

// lipgloss centres a block by its widest line, so a ragged splash would centre
// each line separately and tilt the mark. Every row must be the same width --
// which is why it is padded with U+2800 BRAILLE PATTERN BLANK rather than spaces.
func TestTheSplashIsARectangle(t *testing.T) {
	// LogoMarkSmall is joined HORIZONTALLY against a block of text on the help
	// screen, where a ragged edge leaves the two columns crooked against each
	// other -- the same defect, in the other axis.
	for name, art := range map[string]string{"FallBackText": FallBackText, "LogoMarkSmall": LogoMarkSmall} {
		lines := strings.Split(strings.Trim(art, "\n"), "\n")
		require.NotEmpty(t, lines, name)

		width := runewidth.StringWidth(lines[0])
		require.Greater(t, width, 0, "%s has no width", name)
		for i, line := range lines {
			require.Equal(t, width, runewidth.StringWidth(line),
				"%s row %d is a different width: %q", name, i, line)
		}

		// A blank-padded rectangle would satisfy the above and show nothing, so
		// check the mark is actually in there.
		require.Contains(t, art, "⠋", "%s should contain the rendered mark", name)
	}
}

// capture-pane's trailing newline is not a row. Split on it and the tail-slice
// below keeps a phantom blank line under the newest output and drops the oldest
// real one.
func TestTheCapturesTrailingNewlineIsNotARow(t *testing.T) {
	const width, height = 60, 10
	p := configuredRunPane(t, width, height)

	body := height - headerLines(len(p.status.Lamps))
	rows := make([]string, body)
	for i := range rows {
		rows[i] = fmt.Sprintf("line-%d", i+1)
	}
	p.content = strings.Join(rows, "\n") + "\n"

	rendered := strings.Split(p.String(), "\n")
	require.Len(t, rendered, height)
	require.Contains(t, plain(rendered[height-body]), "line-1", "the oldest line is still on screen")
	require.Contains(t, plain(rendered[height-1]), fmt.Sprintf("line-%d", body), "and the newest is on the last row")
}
