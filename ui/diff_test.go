package ui

import (
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/git"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// diffInstance is a started session for the diff pane to read stats off. The
// pane never touches the session itself, so the mocked tmux plumbing in
// startedInstance is all it needs to satisfy Started().
func diffInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	return startedInstance(t, title, func(string) ([]byte, error) { return []byte(""), nil })
}

// Colourising a diff is a per-line restyle of the whole thing plus a join --
// 18.6ms and 5.5MB allocated for a 3,000-line diff, measured. With the Diff tab
// open the tick recomputes the selected session's diff every few seconds, and
// SetDiff runs after every tick, so re-colourising a diff that came back the
// same was work on the event loop for nothing.
func TestDiffPaneReusesTheRenderUntilTheContentChanges(t *testing.T) {
	pane := NewDiffPane()
	pane.SetSize(80, 20)
	inst := diffInstance(t, "diff-a")

	content := strings.Repeat("+an added line of code\n-a removed line of code\n", 200)
	inst.SetDiffStats(&git.DiffStats{Added: 200, Removed: 200, Content: content}, true)
	pane.SetDiff(inst)
	require.Contains(t, pane.String(), "added line")

	// Recomputed, and the same: nothing to rebuild. A rebuild allocates per line.
	same := &git.DiffStats{Added: 200, Removed: 200, Content: strings.Clone(content)}
	allocs := testing.AllocsPerRun(5, func() {
		inst.SetDiffStats(same, true)
		pane.SetDiff(inst)
	})
	require.Less(t, allocs, 10.0, "an unchanged diff must not be colourised again")

	// Recomputed, and different: the pane rebuilds.
	inst.SetDiffStats(&git.DiffStats{Added: 1, Removed: 0, Content: "+second\n"}, true)
	pane.SetDiff(inst)
	require.Contains(t, pane.String(), "second")
}

// Only the line that is too wide is cut. Every line used to be padded to the
// widest before clipping, so one long line ended every line in "…".
func TestDiffPaneClipsOnlyTheLinesThatAreTooWide(t *testing.T) {
	pane := NewDiffPane()
	pane.SetSize(40, 20)
	inst := diffInstance(t, "diff-a")

	inst.SetDiffStats(&git.DiffStats{Added: 2, Content: "+short\n+" + strings.Repeat("long ", 30) + "\n"}, true)
	pane.SetDiff(inst)

	var clipped int
	for _, line := range strings.Split(ansi.Strip(pane.String()), "\n") {
		if strings.Contains(line, "short") {
			require.NotContains(t, line, "…")
		}
		if strings.Contains(line, "…") {
			clipped++
		}
	}
	require.Equal(t, 1, clipped, "exactly the one long line is cut")
}

// The cached render must not survive a switch to another session, or the pane
// shows one session's diff under another's name.
func TestDiffPaneRebuildsWhenTheSelectionMoves(t *testing.T) {
	pane := NewDiffPane()
	pane.SetSize(80, 20)
	first, second := diffInstance(t, "diff-first"), diffInstance(t, "diff-second")

	first.SetDiffStats(&git.DiffStats{Added: 1, Content: "+mine\n"}, true)
	second.SetDiffStats(&git.DiffStats{Added: 1, Content: "+theirs\n"}, true)

	pane.SetDiff(first)
	require.Contains(t, pane.String(), "mine")
	pane.SetDiff(second)
	require.Contains(t, pane.String(), "theirs")
	require.NotContains(t, pane.String(), "mine")
}

// An empty diff clears the stamp, so the next real one is built rather than
// compared against it.
func TestDiffPaneForgetsOnAnEmptyDiff(t *testing.T) {
	pane := NewDiffPane()
	pane.SetSize(80, 20)
	inst := diffInstance(t, "diff-a")

	inst.SetDiffStats(&git.DiffStats{Added: 1, Content: "+first\n"}, true)
	pane.SetDiff(inst)
	inst.SetDiffStats(&git.DiffStats{}, true)
	pane.SetDiff(inst)
	require.Contains(t, strings.ToLower(pane.String()), "no changes")

	inst.SetDiffStats(&git.DiffStats{Added: 1, Content: "+again\n"}, true)
	pane.SetDiff(inst)
	require.Contains(t, pane.String(), "again")
}

func TestColorizeDiffNamesEachFileAndHidesThePlumbing(t *testing.T) {
	diff := "diff --git a/ui/list.go b/ui/list.go\nindex 1..2 100644\n--- a/ui/list.go\n+++ b/ui/list.go\n" +
		"@@ -1,2 +1,2 @@\n-old\n--- sql comment removed\n+new\n" +
		"diff --git a/new.txt b/new.txt\nnew file mode 100644\nindex 0..3\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+hello\n"
	out, files := colorizeDiff(diff)
	plainOut := plain(out)
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	for _, want := range []string{"▍ ui/list.go +1 −2", "▍ new.txt +1", "new file mode", "--- sql comment removed"} {
		if !strings.Contains(plainOut, want) {
			t.Errorf("output lacks %q:\n%s", want, plainOut)
		}
	}
	for _, gone := range []string{"diff --git", "index 1..2", "+++ b/ui/list.go", "--- /dev/null"} {
		if strings.Contains(plainOut, gone) {
			t.Errorf("plumbing %q should be folded into the file bar", gone)
		}
	}
}

func TestClipLinesKeepsEveryLineToThePane(t *testing.T) {
	out := clipLines("+"+strings.Repeat("x", 200)+"\n-short", 40)
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("line is %d wide, pane is 40", w)
		}
	}
}
