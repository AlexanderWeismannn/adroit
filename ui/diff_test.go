package ui

import (
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/git"

	"github.com/charmbracelet/lipgloss"
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
// 2.4ms and 1.7MB allocated for a 200KB diff, measured. SetDiff runs on every
// preview tick, so doing that again for a diff nobody recomputed was the
// interface's largest source of garbage.
func TestDiffPaneReusesTheRenderUntilTheStatsAreRecomputed(t *testing.T) {
	pane := NewDiffPane()
	pane.SetSize(80, 20)
	inst := diffInstance(t, "diff-a")

	inst.SetDiffStats(&git.DiffStats{Added: 1, Removed: 0, Content: "+first\n"}, true)
	pane.SetDiff(inst)
	require.Contains(t, pane.String(), "first")

	// Same stamp, different content: nothing recomputed the stats, so the pane is
	// entitled to keep what it built.
	inst.GetDiffStats().Content = "+second\n"
	pane.SetDiff(inst)
	require.NotContains(t, pane.String(), "second")

	// A recompute moves the stamp, and the pane rebuilds.
	inst.SetDiffStats(&git.DiffStats{Added: 1, Removed: 0, Content: "+second\n"}, true)
	pane.SetDiff(inst)
	require.Contains(t, pane.String(), "second")
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
