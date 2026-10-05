package ui

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"github.com/AlexanderWeismannn/adroit/theme"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func newTestList(titles ...string) *List {
	s := spinner.New()
	l := NewList(&s, false)
	for _, t := range titles {
		inst, _ := session.NewInstance(session.InstanceOptions{
			Title:   t,
			Path:    ".",
			Program: "echo",
		})
		l.AddInstance(inst)
	}
	return l
}

func TestMoveUp(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveUp()
	require.True(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "b", l.items[0].Title)
	require.Equal(t, "a", l.items[1].Title)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveUp_AtTop(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(0)

	moved := l.MoveUp()
	require.False(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
}

func TestMoveDown(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveDown()
	require.True(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
	require.Equal(t, "c", l.items[1].Title)
	require.Equal(t, "b", l.items[2].Title)
}

func TestMoveDown_AtBottom(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(2)

	moved := l.MoveDown()
	require.False(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveWithSingleItem(t *testing.T) {
	l := newTestList("only")
	l.SetSelectedInstance(0)

	require.False(t, l.MoveUp())
	require.False(t, l.MoveDown())
}

// newTestRenderer builds a renderer with a known width plus one instance whose
// CI verdict the caller controls.
func newTestRenderer(t *testing.T, width int, branch string, status ci.Status) (*InstanceRenderer, *session.Instance) {
	t.Helper()
	s := spinner.New()
	r := &InstanceRenderer{spinner: &s}
	r.setWidth(width)

	inst, err := session.NewInstance(session.InstanceOptions{Title: "session", Path: ".", Program: "echo"})
	require.NoError(t, err)
	inst.Branch = branch
	inst.SetCIStatus(status)
	return r, inst
}

// branchLineOf returns the rendered line carrying the branch name.
func branchLineOf(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, branchIcon) {
			return line
		}
	}
	t.Fatalf("no branch line in rendered output %q", out)
	return ""
}

func TestRenderCIBadge(t *testing.T) {
	tests := []struct {
		name   string
		status ci.Status
		want   string
	}{
		{
			name:   "failure shows the glyph and PR number",
			status: ci.Status{State: ci.StateFailure, PRNumber: 4420, Failed: 1},
			want:   ciFailureIcon + " #4420 failed",
		},
		{
			name:   "success shows the glyph and PR number",
			status: ci.Status{State: ci.StateSuccess, PRNumber: 12, Passed: 7},
			want:   ciSuccessIcon + " #12 passed",
		},
		{
			name:   "still running shows the pending glyph",
			status: ci.Status{State: ci.StatePending, PRNumber: 99, Pending: 3},
			want:   ciPendingIcon + " #99 running",
		},
		{
			name:   "a branch with no pull request says so",
			status: ci.Status{State: ci.StateNoPR},
			want:   ciNoPRIcon + " no PR",
		},
		{
			// Not the PR number: nothing more is going to happen to it, and a merged
			// PR is all-green, so a bare tick would read as "open and passing".
			name:   "a merged pull request says merged, not passed",
			status: ci.Status{State: ci.StateMerged, PRNumber: 4418},
			want:   ciMergedIcon + " merged",
		},
		{
			name:   "a closed pull request says closed",
			status: ci.Status{State: ci.StateClosed, PRNumber: 4418},
			want:   ciClosedIcon + " closed",
		},
		{
			name:   "a conflicting branch is marked even while green",
			status: ci.Status{State: ci.StateSuccess, PRNumber: 4406, Passed: 7, Mergeable: ci.MergeConflicting},
			want:   ciSuccessIcon + " #4406 passed " + ciConflictIcon + " conflict",
		},
		{
			name:   "changes requested is marked",
			status: ci.Status{State: ci.StateSuccess, PRNumber: 4420, Review: ci.ReviewChangesRequested},
			want:   ciSuccessIcon + " #4420 passed " + ciChangesIcon + " changes",
		},
		{
			name:   "an approved pull request is marked",
			status: ci.Status{State: ci.StateSuccess, PRNumber: 4419, Review: ci.ReviewApproved},
			want:   ciSuccessIcon + " #4419 passed " + ciApprovedIcon + " approved",
		},
		{
			// The two facts are independent, and both need acting on.
			name:   "a red build and an approval are shown together",
			status: ci.Status{State: ci.StateFailure, PRNumber: 4419, Review: ci.ReviewApproved},
			want:   ciFailureIcon + " #4419 failed " + ciApprovedIcon + " approved",
		},
		{
			// Nothing is going to change about a merged PR, so its review verdict
			// is history -- and the state word leaves no room for a marker anyway.
			name:   "a merged pull request carries no marker",
			status: ci.Status{State: ci.StateMerged, PRNumber: 4418, Review: ci.ReviewApproved},
			want:   ciMergedIcon + " merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, inst := newTestRenderer(t, 80, "feature", tt.status)
			require.Contains(t, branchLineOf(t, r.Render(inst, 1, false, false, false)), tt.want)
		})
	}
}

// A pull request can be conflicting, unapproved and red all at once, but there
// is room for one marker. Order it by what has to happen next.
func TestPRMarkerPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		status ci.Status
		want   string
	}{
		{
			// A conflict blocks the merge whatever the reviewers said, and is the
			// author's to fix either way.
			name:   "a conflict outranks changes requested",
			status: ci.Status{Mergeable: ci.MergeConflicting, Review: ci.ReviewChangesRequested},
			want:   ciConflictIcon,
		},
		{
			name:   "a conflict outranks an approval",
			status: ci.Status{Mergeable: ci.MergeConflicting, Review: ci.ReviewApproved},
			want:   ciConflictIcon,
		},
		{
			// The normal state of a fresh pull request. A marker on every row would
			// say nothing.
			name:   "no review and no conflict earns no marker",
			status: ci.Status{Mergeable: ci.MergeClean, Review: ci.ReviewNone},
			want:   "",
		},
		{
			// GitHub answers UNKNOWN for the seconds after a push, which must not
			// read as a conflict.
			name:   "unknown mergeability is not a conflict",
			status: ci.Status{Mergeable: ci.MergeUnknown, Review: ci.ReviewApproved},
			want:   ciApprovedIcon,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon, _, _ := prMarker(tt.status)
			require.Equal(t, tt.want, icon)
		})
	}
}

// StateUnknown covers three cases that must all look identical: the feature is
// disabled, gh is unavailable, and the first lookup has not come back yet. None
// of them may draw a badge, or the list would claim a verdict it does not have.
func TestRenderNoBadgeWithoutAVerdict(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "feature", ci.Status{})
	line := branchLineOf(t, r.Render(inst, 1, false, false, false))

	for _, icon := range []string{ciSuccessIcon, ciFailureIcon, ciPendingIcon, ciMergedIcon, ciClosedIcon, ciNoPRIcon} {
		require.NotContains(t, line, icon)
	}
}

// The badge is reserved out of the line's width budget before the branch name is
// truncated, so adding one must not change the rendered width at all -- an
// overlong branch has to give way instead. Regression guard for the width
// accounting in Render.
//
// Compared against the no-badge baseline rather than against r.width, because
// the branch line already exceeds r.width by 2 upstream: listDescStyle carries
// Padding(0, 1, 1, 1) and remainingWidth never subtracts it. That is not this
// feature's bug to encode as correct, so the assertion is "the badge costs
// nothing", which is what this change is responsible for.
func TestRenderCIBadgeDoesNotChangeLineWidth(t *testing.T) {
	const longBranch = "a-very-long-branch-name-that-cannot-possibly-fit-on-one-line"

	badged := []ci.Status{
		{State: ci.StateNoPR},
		{State: ci.StateMerged, PRNumber: 4418},
		{State: ci.StateClosed, PRNumber: 4418},
		{State: ci.StateFailure, PRNumber: 4420},
		{State: ci.StatePending, PRNumber: 7},
		{State: ci.StateSuccess, PRNumber: 123456},
		// The markers are the widest the badge gets, so they are the cases most
		// likely to push the branch name off the line.
		{State: ci.StateSuccess, PRNumber: 123456, Mergeable: ci.MergeConflicting},
		{State: ci.StateFailure, PRNumber: 4420, Review: ci.ReviewChangesRequested},
		{State: ci.StatePending, PRNumber: 4419, Review: ci.ReviewApproved},
	}

	for _, width := range []int{20, 30, 40, 60, 80, 120} {
		base, baseInst := newTestRenderer(t, width, longBranch, ci.Status{})
		baseWidth := lipgloss.Width(branchLineOf(t, base.Render(baseInst, 1, false, false, false)))

		for _, status := range badged {
			r, inst := newTestRenderer(t, width, longBranch, status)
			line := branchLineOf(t, r.Render(inst, 1, false, false, false))
			require.Equal(t, baseWidth, lipgloss.Width(line),
				"width %d, state %v: badge changed the line width", width, status.State)
			// Anchored on the glyph and its separator; the separator used to be a
			// hyphen, which read as part of the branch name.
			require.Contains(t, line, branchIcon+" "+runewidth.Truncate(longBranch, 3, ""),
				"width %d, state %v: branch name vanished entirely", width, status.State)
		}
	}
}

// A bare space sitting after an already-styled span renders with the terminal's
// own background rather than the row's, which showed up as a black block in the
// middle of the highlighted row. Guard the whole class rather than the one space:
// on a selected row, no reset may be immediately followed by an unstyled space.
func TestRenderSelectedRowHasNoUnstyledGaps(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, status := range []ci.Status{
		{State: ci.StateNoPR},
		{State: ci.StateMerged, PRNumber: 4418},
		{State: ci.StateClosed, PRNumber: 4418},
		{State: ci.StatePending, PRNumber: 4420},
		{State: ci.StateFailure, PRNumber: 4420},
		{State: ci.StateSuccess, PRNumber: 4420},
	} {
		r, inst := newTestRenderer(t, 80, "task-5636-vis-6", status)
		inst.SetDiffStats(&git.DiffStats{Added: 9997, Removed: 192}, false)

		line := branchLineOf(t, r.Render(inst, 1, true, false, false))
		require.NotContains(t, line, "\x1b[0m ",
			"state %v: a style reset is followed by an unstyled space, which renders as a gap in the row highlight",
			status.State)
	}
}

// The label has to track the branch the worktree really has checked out, or a
// renamed branch shows the old name beside the new branch's CI verdict — the two
// halves of the row disagreeing about which branch it is.
func TestRenderShowsTheResolvedBranch(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "task-old-name", ci.Status{State: ci.StateSuccess, PRNumber: 4420})
	inst.SetCurrentBranch("TASK-5636-Vis-6")

	line := branchLineOf(t, r.Render(inst, 1, false, false, false))
	require.Contains(t, line, "TASK-5636-Vis-6")
	require.NotContains(t, line, "task-old-name")
}

// The accent bar takes the column the left padding used to occupy, so a selected
// row must be exactly as wide as an unselected one. Getting this wrong shifts
// every line of the list by a column as the selection moves, which is the kind of
// thing that looks like a rendering bug rather than a style choice.
func TestSelectedRowMatchesUnselectedWidth(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, width := range []int{20, 30, 40, 60, 80, 120} {
		for _, status := range []ci.Status{
			{},
			{State: ci.StateNoPR},
			{State: ci.StateFailure, PRNumber: 4420},
			{State: ci.StateSuccess, PRNumber: 4419, Review: ci.ReviewApproved},
			{State: ci.StateMerged, PRNumber: 4418},
		} {
			r, inst := newTestRenderer(t, width, "TASK-5636-Vis-6", status)

			for i, line := range strings.Split(r.Render(inst, 1, false, false, false), "\n") {
				sel := strings.Split(r.Render(inst, 1, true, false, false), "\n")
				require.Equal(t, lipgloss.Width(line), lipgloss.Width(sel[i]),
					"width %d, state %v, line %d: selection changed the row width", width, status.State, i)
			}
		}
	}
}

// "+2200,-12" reads as one token; they are two separate quantities.
func TestDiffStatsAreSpaceSeparated(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "feature", ci.Status{})
	inst.SetDiffStats(&git.DiffStats{Added: 2200, Removed: 12}, false)

	line := branchLineOf(t, r.Render(inst, 1, false, false, false))
	require.Contains(t, line, "+2.2k -12")
	require.NotContains(t, line, "+2.2k,-12")
}

func TestCompactCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1.0k", 2387: "2.4k", 9999: "10k", 10000: "10k", 25064: "25k"} {
		require.Equal(t, want, compactCount(n), "compactCount(%d)", n)
	}
}

// "Ꮧ-TASK-5636" reads as though the hyphen were part of the branch name.
func TestBranchGlyphIsNotFollowedByAHyphen(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5636-Vis-6", ci.Status{})
	line := branchLineOf(t, r.Render(inst, 1, false, false, false))
	require.Contains(t, line, branchIcon+" TASK-5636-Vis-6")
	require.NotContains(t, line, branchIcon+"-")
}

// The legend exists so the glyphs are documented somewhere. Its value depends on
// staying complete, and the failure mode of an incomplete one is silent — a new
// badge simply goes unexplained — so assert every glyph the list can draw is in
// it. Add the glyph to BadgeLegend when this fails; do not delete the case.
func TestBadgeLegendCoversEveryGlyph(t *testing.T) {
	legend := strings.Join(BadgeLegend(), "\n")

	for _, icon := range []string{
		strings.TrimSpace(readyIcon), strings.TrimSpace(pausedIcon),
		spinnerLegendFrame, strings.TrimSpace(doneChip),
		ciSuccessIcon, ciFailureIcon, ciPendingIcon, ciMergedIcon, ciClosedIcon, ciNoPRIcon,
		ciConflictIcon, ciChangesIcon, ciApprovedIcon,
		upstreamBehindIcon, upstreamAheadIcon, ciCommentIcon,
	} {
		require.Contains(t, legend, icon, "glyph %q is drawn in the list but absent from the legend", icon)
	}
}

// The count is otherwise only obtainable by counting rows.
func TestListHeaderShowsTheSessionCount(t *testing.T) {
	l := &List{renderer: &InstanceRenderer{}}
	l.SetSize(80, 40)

	// Nothing to count yet: "· 0" says less than the preview pane's empty state.
	require.NotContains(t, l.String(), "·")

	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	l.AddInstance(inst)
	require.Contains(t, l.String(), "Instances · 1")
}

// The line means something different for a session with no branch: a directory,
// not a branch. Naming the repository's current HEAD there would claim a branch
// that is not the session's and can change under it.
func TestRepoSessionRowShowsTheDirectory(t *testing.T) {
	plain, err := session.NewInstance(session.InstanceOptions{
		Title: "scratch", Path: ".", Program: "echo", NoWorktree: true,
	})
	require.NoError(t, err)
	plain.Branch = "should-not-be-shown"

	s := spinner.New()
	r := &InstanceRenderer{spinner: &s}
	r.setWidth(80)

	// Not branchLineOf: that finds the line by its branch glyph, and the whole
	// point is that this row does not carry one.
	line := lineContaining(t, r.Render(plain, 1, false, false, false), repoIcon)
	require.Contains(t, line, repoIcon)
	require.NotContains(t, line, branchIcon)
	require.NotContains(t, line, "should-not-be-shown")
	require.Contains(t, line, filepath.Base(mustAbs(t, ".")))
}

func lineContaining(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in %q", needle, out)
	return ""
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	require.NoError(t, err)
	return abs
}

// The styles hold a copy of their colour, so a theme only takes effect if the
// rebuild hooks registered in init actually run. Without that this whole feature
// would configure cleanly and change nothing on screen.
func TestThemeChangeReachesTheRenderedRow(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	before := theme.Current()
	t.Cleanup(func() {
		theme.Set(before)
		lipgloss.SetColorProfile(previous)
	})

	r, inst := newTestRenderer(t, 80, "TASK-1", ci.Status{State: ci.StateSuccess, PRNumber: 1})
	base := r.Render(inst, 1, true, false, false)

	dracula, ok := theme.Builtin("dracula")
	require.True(t, ok)
	theme.Set(dracula)
	themed := r.Render(inst, 1, true, false, false)

	require.NotEqual(t, base, themed, "the theme did not reach the rendered row")
	// The change must be colour only: a palette cannot move a character.
	require.Equal(t, stripANSI(base), stripANSI(themed))
	require.Contains(t, themed, "80;250;123", "dracula's success green (#50fa7b) should be in the passing badge")
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// Feedback is not exclusive with the review verdict: a pull request is routinely
// approved AND still carrying remarks. Collapsing them into one marker slot would
// hide whichever lost.
func TestTheBadgeShowsAReviewVerdictAndFeedbackTogether(t *testing.T) {
	status := ci.Status{
		State:    ci.StateSuccess,
		PRNumber: 4471,
		Review:   ci.ReviewApproved,
		Feedback: ci.FeedbackOutstanding,
	}
	plainBadge, _ := ciBadge(status, lipgloss.NoColor{}, false)
	require.Equal(t, "✓ #4471 ★ ❞", plainBadge)
}

// The marker is dropped where a remark is history: a merged pull request's
// conversation is over, and a closed one's is moot.
func TestATerminalPullRequestCarriesNoFeedbackMarker(t *testing.T) {
	for _, state := range []ci.State{ci.StateMerged, ci.StateClosed, ci.StateNoPR} {
		plainBadge, _ := ciBadge(ci.Status{State: state, Feedback: ci.FeedbackOutstanding}, lipgloss.NoColor{}, false)
		require.NotContains(t, plainBadge, ciCommentIcon, "state %v", state)
	}
}

// The two halves of the badge must stay in step: plain is what the row subtracts
// from the branch name's width budget, so a span added to one and not the other
// overflows the pane.
func TestTheFeedbackMarkerIsCountedInTheBadgeWidth(t *testing.T) {
	quiet, _ := ciBadge(ci.Status{State: ci.StateSuccess, PRNumber: 4473}, lipgloss.NoColor{}, false)
	noisy, _ := ciBadge(ci.Status{State: ci.StateSuccess, PRNumber: 4473, Feedback: ci.FeedbackAddressed}, lipgloss.NoColor{}, false)

	require.Equal(t, runewidth.StringWidth(quiet)+2, runewidth.StringWidth(noisy),
		"the marker and its leading space must be in the width the caller budgets")
}

// Outstanding and addressed share a glyph and differ by colour, so the styled
// half has to actually differ -- otherwise the distinction is invisible.
func TestOutstandingAndAddressedFeedbackAreStyledDifferently(t *testing.T) {
	outstanding, _ := feedbackMarker(ci.Status{Feedback: ci.FeedbackOutstanding})
	addressed, _ := feedbackMarker(ci.Status{Feedback: ci.FeedbackAddressed})
	none, _ := feedbackMarker(ci.Status{Feedback: ci.FeedbackNone})

	require.Equal(t, ciCommentIcon, outstanding)
	require.Equal(t, ciCommentIcon, addressed)
	require.Empty(t, none)

	_, outStyle := feedbackMarker(ci.Status{Feedback: ci.FeedbackOutstanding})
	_, addStyle := feedbackMarker(ci.Status{Feedback: ci.FeedbackAddressed})
	require.NotEqual(t, outStyle.GetForeground(), addStyle.GetForeground(),
		"the two states must not render identically")
}

func listOfSize(t *testing.T, n, width, height int) *List {
	t.Helper()
	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&spin, false)
	repo := t.TempDir()
	for i := 0; i < n; i++ {
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: fmt.Sprintf("TASK-57%02d-a-session", i), Path: repo,
			Status: session.Paused, Program: "claude", NoWorktree: true})
		require.NoError(t, err)
		l.AddInstance(inst)()
	}
	l.SetSize(width, height)
	return l
}

// The list drew every session and let the surplus fall off the bottom of the
// terminal: twelve sessions in a forty-row pane rendered sixty-eight rows, and
// the five that did not fit were unreachable -- no scroll, and nothing to say
// they existed.
func TestTheListFitsThePaneItIsGiven(t *testing.T) {
	for _, height := range []int{40, 30, 20, 12} {
		l := listOfSize(t, 12, 50, height)
		for _, sel := range []int{0, 5, 11} {
			l.SetSelectedInstance(sel)
			rows := strings.Split(l.String(), "\n")
			require.LessOrEqual(t, len(rows), height,
				"height %d, selection %d: rendered %d rows", height, sel, len(rows))
		}
	}
}

// Scrolling exists to keep the selection reachable, so the selected session must
// be on screen wherever it is in the list.
func TestTheSelectedSessionIsAlwaysVisible(t *testing.T) {
	l := listOfSize(t, 12, 50, 24)
	for sel := 0; sel < 12; sel++ {
		l.SetSelectedInstance(sel)
		want := fmt.Sprintf("TASK-57%02d-a-session", sel)
		require.Contains(t, plain(l.String()), want, "selection %d fell off the pane", sel)
	}
	// And going back up brings it back into view.
	for sel := 11; sel >= 0; sel-- {
		l.SetSelectedInstance(sel)
		require.Contains(t, plain(l.String()), fmt.Sprintf("TASK-57%02d-a-session", sel),
			"selection %d fell off on the way back up", sel)
	}
}

// Rows that are not drawn have to be accounted for, or the list silently claims
// to be the whole of itself.
func TestHiddenSessionsAreCounted(t *testing.T) {
	l := listOfSize(t, 12, 50, 24)
	l.SetSelectedInstance(6)
	rendered := plain(l.String())

	require.Regexp(t, `↑ \d+ more`, rendered, "sessions above the window must be counted")
	require.Regexp(t, `↓ \d+ more`, rendered, "sessions below the window must be counted")

	// The two counts plus what is drawn must be the whole list.
	above := regexp.MustCompile(`↑ (\d+) more`).FindStringSubmatch(rendered)
	below := regexp.MustCompile(`↓ (\d+) more`).FindStringSubmatch(rendered)
	drawn := strings.Count(rendered, "-a-session")
	a, _ := strconv.Atoi(above[1])
	b, _ := strconv.Atoi(below[1])
	require.Equal(t, 12, a+b+drawn, "counted %d above + %d below + %d drawn", a, b, drawn)
}

// A list that fits must not be scrolled, or it would give up two rows and hide
// two sessions for nothing.
func TestAListThatFitsIsNotScrolled(t *testing.T) {
	l := listOfSize(t, 3, 50, 40)
	rendered := plain(l.String())
	require.NotContains(t, rendered, "more")
	for i := 0; i < 3; i++ {
		require.Contains(t, rendered, fmt.Sprintf("TASK-57%02d-a-session", i))
	}
}

// A branch that only restates the title is worse than nothing there: it read
// "Ꮧ TASK-5..." beneath a title that already said "TASK-5633-Remove-Old-RBAC",
// truncated past the point of meaning.
func TestBranchEchoesTitle(t *testing.T) {
	cases := []struct {
		branch, title string
		want          bool
	}{
		{"TASK-5719-Entity-Matching", "TASK-5719-Entity-Matching", true},
		// The configured prefix is exactly the difference between the two.
		{"TASK-5766-Zebra-Dynamics", "5766-Zebra-Dynamics", true},
		// Lower-casing is what sanitising does with preserve_branch_case off.
		{"task-5766-zebra", "5766-Zebra", true},
		{"feature/unrelated", "TASK-5719", false},
		{"", "TASK-5719", false},
		{"TASK-5719", "", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, branchEchoesTitle(c.branch, c.title),
			"branchEchoesTitle(%q, %q)", c.branch, c.title)
	}
}

// And the width it took came out of the badge's budget, which is dropped FIRST
// when space is short -- so the half that told you something was the half that
// disappeared. With the echo gone the badge keeps the line.
func TestDroppingTheEchoedBranchKeepsTheBadge(t *testing.T) {
	sp := spinner.New()
	r := &InstanceRenderer{spinner: &sp}
	r.setWidth(46)

	inst, err := session.FromInstanceData(session.InstanceData{
		Title: "TASK-5633-Remove-Old-RBAC", Path: t.TempDir(), Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(),
			BranchName: "TASK-5633-Remove-Old-RBAC", SessionName: "TASK-5633-Remove-Old-RBAC"},
	})
	require.NoError(t, err)
	inst.SetCurrentBranch("TASK-5633-Remove-Old-RBAC")
	inst.SetCIStatus(ci.Status{State: ci.StateSuccess, PRNumber: 4472, Feedback: ci.FeedbackOutstanding})

	out := r.Render(inst, 1, false, false, false)
	// Not branchLineOf: that finds the line by its branch glyph, and a row whose
	// branch was dropped for saying nothing no longer carries one.
	line := lineContaining(t, out, "#4472")
	require.Contains(t, line, "#4472", "the pull request must survive on a narrow row")
	require.NotContains(t, line, "TASK-5633", "the branch echo must be gone")
	require.NotContains(t, plain(out), branchIcon, "and the glyph goes with it")
}

// A session being named used to render as a bare number and an empty branch
// glyph: you typed into a row that showed nothing back, with the footer as the
// only sign anything was happening.
func TestTheNamingRowShowsWhatIsHappening(t *testing.T) {
	sp := spinner.New()
	r := &InstanceRenderer{spinner: &sp}
	r.setWidth(46)

	inst, err := session.NewInstance(session.InstanceOptions{Title: "", Path: ".", Program: "echo"})
	require.NoError(t, err)

	r.naming, r.branchPreview = true, ""
	empty := plain(r.Render(inst, 6, true, false, true))
	require.Contains(t, empty, namingCursor, "a cursor says where the typing goes")
	require.Contains(t, empty, "name this session", "and a placeholder says what is wanted")
	require.NotContains(t, empty, readyIcon,
		"a session that does not exist yet must not claim a status")
	// The second line previews the branch, and with nothing typed there is no
	// branch to preview -- a blank band under the cursor that reads as a
	// half-drawn row rather than as a field waiting for input.
	require.Contains(t, empty, namingBranchHint, "the empty preview line has to say what it is for")

	// Typing previews the branch the name will create -- the one place the prefix
	// and sanitising rules are visible before they have already been applied.
	require.NoError(t, inst.SetTitle("my-feature"))
	r.branchPreview = "TASK-my-feature"
	typed := plain(r.Render(inst, 6, true, false, true))
	require.Contains(t, typed, "my-feature"+namingCursor)
	require.Contains(t, typed, "TASK-my-feature")

	// Only the row being named: everything else renders as before.
	r.naming = true
	other := plain(r.Render(inst, 2, false, false, false))
	require.NotContains(t, other, namingCursor)
}

// The row being named is a selected row, so it is also a continuous bar -- and
// it was the one row that was not.
//
// The placeholder and the cursor were styled spans inside a cell lipgloss.Place
// padded with plain spaces, and the status mark spent its two columns on an
// empty string. Each ends in a reset, so from the placeholder rightwards the
// row fell back to the terminal's own background: a dark input-box-shaped bar
// through the middle of the highlight, and a notch out of its corner.
func TestTheNamingRowIsStillAnUnbrokenBar(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	before := theme.Current()
	t.Cleanup(func() { theme.Set(before) })
	solarized, ok := theme.Builtin("solarized")
	require.True(t, ok)
	theme.Set(solarized)

	sp := spinner.New()
	r := &InstanceRenderer{spinner: &sp}

	for _, width := range []int{30, 46, 80, 120} {
		r.setWidth(width)
		named, err := session.NewInstance(session.InstanceOptions{Title: "", Path: ".", Program: "echo"})
		require.NoError(t, err)
		settled, err := session.NewInstance(session.InstanceOptions{Title: "session", Path: ".", Program: "echo"})
		require.NoError(t, err)
		settled.Status = session.Ready

		for _, name := range []string{"", "my-feature"} {
			require.NoError(t, named.SetTitle(name))
			r.naming, r.branchPreview = true, ""
			if name != "" {
				r.branchPreview = "TASK-" + name
			}

			rows := strings.Split(r.Render(named, 5, true, false, true), "\n")
			for n, line := range rows {
				require.Empty(t, unbackedRunes(line),
					"width %d, name %q, line %d: the naming row's bar is broken", width, name, n)
			}
			// And it stays the width of every other row: the status mark's column is
			// spent while naming, not reclaimed, so the row must not shrink.
			r.naming = false
			for n, line := range strings.Split(r.Render(settled, 1, true, false, false), "\n") {
				require.Equal(t, lipgloss.Width(line), lipgloss.Width(rows[n]),
					"width %d, name %q, line %d: naming changed the row width", width, name, n)
			}
		}
	}
}

// The list and the pane beside it both led with the same blank gap, so every
// screen spent a row on the same piece of nothing twice over. Trimmed together
// they stay level -- which is the constraint: the session rows have to start
// where the pane's content starts, or the two columns read as unrelated.
func TestTheListAndTheTabbedWindowStartLevel(t *testing.T) {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	l := NewList(&sp, false)
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: "TASK-5700-a-session", Path: t.TempDir(), Status: session.Paused,
		Program: "claude", NoWorktree: true})
	require.NoError(t, err)
	l.AddInstance(inst)()

	w := NewTabbedWindow(NewPreviewPane(), NewDiffPane(), NewTerminalPane(), NewRunPane(dev.New(nil)))
	const width, height = 50, 30
	l.SetSize(width, height)
	w.SetSize(width, height)

	firstRow := func(lines []string, match func(string) bool) int {
		for i, line := range lines {
			if match(plain(line)) {
				return i
			}
		}
		return -1
	}

	listLines := strings.Split(l.String(), "\n")
	tabLines := strings.Split(w.String(), "\n")

	titleRow := firstRow(listLines, func(s string) bool { return strings.Contains(s, "Instances") })
	tabTopRow := firstRow(tabLines, func(s string) bool { return strings.Contains(s, "╭") })
	require.Equal(t, tabTopRow, titleRow, "the list title and the tab bar must start on the same row")

	// And exactly one blank row leads both, rather than two.
	require.Equal(t, 1, titleRow, "one blank row above the title, not two")
}

// The headline behaviour. A session that has ended its turn with a shell still
// running must not look finished: it will wake up and carry on by itself, and the
// ready dot is an invitation to carry on yourself.
func TestShellStillRunningDoesNotLookFinished(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{})
	inst.Status = session.Ready
	inst.SetActivity(session.ActivityShell, 1)

	out := r.Render(inst, 1, false, false, false)
	require.NotContains(t, out, strings.TrimSpace(readyIcon),
		"a session with a shell still running rendered the ready dot")
	require.Contains(t, out, r.spinner.View(), "the row should still be animating")
}

// The chip is what makes a finish visible to someone who was reading another
// pane, and it has to expire on its own -- a list left alone becomes a wall of
// chips saying nothing. What it settles to is the unseen mark, not the ready
// dot: the finish is still news until the row has been looked at, and coming
// back after ten minutes must still say which sessions finished meanwhile.
func TestJustFinishedChipExpires(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{})
	inst.Status = session.Ready
	inst.SetActivity(session.ActivityWorking, 0)
	inst.SetActivity(session.ActivityIdle, 0)
	finished := inst.DoneAt()

	r.now = func() time.Time { return finished.Add(doneChipWindow - time.Second) }
	require.Contains(t, r.Render(inst, 1, false, false, false), strings.TrimSpace(doneChip))

	r.now = func() time.Time { return finished.Add(doneChipWindow + time.Second) }
	out := r.Render(inst, 1, false, false, false)
	require.NotContains(t, out, strings.TrimSpace(doneChip))
	require.Contains(t, out, strings.TrimSpace(unseenIcon), "it should settle to the unseen mark")
	require.NotContains(t, out, strings.TrimSpace(readyIcon))

	// The selected row is in the preview, so it is not unseen.
	require.Contains(t, r.Render(inst, 1, true, false, false), strings.TrimSpace(readyIcon))

	inst.Acknowledge()
	require.Contains(t, r.Render(inst, 1, false, false, false), strings.TrimSpace(readyIcon),
		"once looked at, it is only ready")
}

// A session stopped on a question keeps saying so, however long it waits: unlike
// a finish, this is not news that goes stale but a turn that cannot go on.
func TestNeedsInputIsMarkedUntilAnswered(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{})
	inst.Status = session.Ready
	idle := lipgloss.Width(r.Render(inst, 1, false, false, false))
	inst.SetActivity(session.ActivityWorking, 0)
	inst.SetActivity(session.ActivityNeedsInput, 0)
	r.now = func() time.Time { return time.Now().Add(time.Hour) }

	out := r.Render(inst, 1, false, false, false)
	require.Contains(t, out, strings.TrimSpace(needsInputChip))
	require.NotContains(t, out, r.spinner.View(), "a session waiting on you is not working")
	require.Equal(t, idle, lipgloss.Width(out), "the chip takes its room out of the title")
	require.True(t, inst.DoneAt().IsZero(), "a question is not a finished turn")
}

// The chip is a word where every other mark is a single glyph, and like the
// dev-stack mark it takes its room out of the title's column. If it did not, the
// list would visibly jump sideways every time a session ended.
func TestJustFinishedChipDoesNotChangeRowWidth(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{})
	inst.Status = session.Ready
	idle := lipgloss.Width(r.Render(inst, 1, false, false, false))

	inst.SetActivity(session.ActivityWorking, 0)
	inst.SetActivity(session.ActivityIdle, 0)
	r.now = inst.DoneAt

	require.Contains(t, r.Render(inst, 1, false, false, false), strings.TrimSpace(doneChip))
	require.Equal(t, idle, lipgloss.Width(r.Render(inst, 1, false, false, false)))
}

// Moving onto a row is how a finish is acknowledged, so the mark must survive the
// cursor merely sitting elsewhere and must not survive arriving. Nor leaving: the
// row the cursor was on was in the preview, so its finish has been seen.
func TestSelectingARowAcknowledgesItsFinish(t *testing.T) {
	l := &List{renderer: &InstanceRenderer{}}
	l.SetSize(80, 40)

	var insts []*session.Instance
	for _, title := range []string{"first", "second"} {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		l.AddInstance(inst)
		inst.SetActivity(session.ActivityWorking, 0)
		inst.SetActivity(session.ActivityIdle, 0)
		insts = append(insts, inst)
	}

	// AddInstance selects as it goes, so start from a known cursor and re-stamp.
	l.SetSelectedInstance(0)
	for _, inst := range insts {
		inst.SetActivity(session.ActivityWorking, 0)
		inst.SetActivity(session.ActivityIdle, 0)
	}

	require.False(t, insts[1].DoneAt().IsZero(), "an unvisited finish should stand")
	l.Down()
	require.True(t, insts[1].DoneAt().IsZero(), "arriving on the row should acknowledge it")
	require.True(t, insts[0].DoneAt().IsZero(), "the row left behind was on screen, so it is seen too")
}

// unbackedRunes returns the printable characters on a rendered line that are
// drawn with no background colour set.
//
// A selected row is a continuous bar, and the way it stops being one is subtle:
// every segment of the row is rendered with its own style, and lipgloss closes
// each one with a reset, so any plain string joined between two styled segments
// falls back to the terminal's own background and punches a hole through the bar.
func unbackedRunes(line string) string {
	var out []rune
	backed := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				break
			}
			for _, code := range strings.Split(line[i+2:i+end], ";") {
				switch {
				case code == "0", code == "", code == "49":
					backed = false
				case strings.HasPrefix(code, "4"):
					backed = true
				}
			}
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if !backed && r != '\n' {
			out = append(out, r)
		}
		i += size
	}
	return string(out)
}

// The selection bar has to be unbroken. It broke between the dev-stack mark and
// the status mark, which are styled separately with a plain space joined between
// them: on a Solarized selected row that space showed as a dark notch in the
// middle of the bar.
func TestSelectedRowHasNoBackgroundGaps(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	before := theme.Current()
	t.Cleanup(func() { theme.Set(before) })
	solarized, ok := theme.Builtin("solarized")
	require.True(t, ok)
	theme.Set(solarized)

	r, inst := newTestRenderer(t, 80, "TASK-5635-UI", ci.Status{State: ci.StateFailure, PRNumber: 4540})
	inst.Status = session.Ready
	// The mark is what precedes the gap, so the row has to be carrying it.
	r.devStackTitle = inst.Title
	r.devStackState = dev.LampUp

	for n, line := range strings.Split(r.Render(inst, 2, true, false, false), "\n") {
		require.Empty(t, unbackedRunes(line),
			"line %d of a selected row has cells with no background: the bar is broken", n)
	}
}

// The glyph labels a name. With the name dropped -- an echo of the title, or a
// pane too narrow to fit it -- it labels an empty space, and a stray mark under
// the title reads as a rendering fault rather than as an icon.
func TestTheBranchGlyphGoesWithItsName(t *testing.T) {
	render := func(t *testing.T, title, branch string) string {
		t.Helper()
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		inst.Branch = branch
		inst.SetDiffStats(&git.DiffStats{Added: 6500, Removed: 193}, false)

		sp := spinner.New()
		r := &InstanceRenderer{spinner: &sp}
		r.setWidth(80)
		return r.Render(inst, 2, false, false, false)
	}

	// The real case: a configured TASK- prefix applied to a branch that already
	// carries it, under a title that already said the same thing.
	echoed := render(t, "TASK-5635-UI", "TASK-TASK-5635-UI")
	require.NotContains(t, plain(echoed), branchIcon,
		"a branch dropped for saying nothing should take its glyph with it")

	// The guard has to be the empty name, not the glyph: a branch worth showing
	// still gets one.
	named := render(t, "session", "TASK-5766-Zebra-Dynamics")
	require.Contains(t, plain(named), branchIcon+" TASK-5766-Zebra-Dynamics")

	// The width the glyph would have taken stays spent rather than reclaimed, so
	// the diff stats sit in the same column on both rows. Reclaiming it would slide
	// them sideways on whichever rows happened to have an echoed branch.
	require.Equal(t, lipgloss.Width(named), lipgloss.Width(echoed))

	columnOf := func(t *testing.T, out, needle string) int {
		t.Helper()
		line := lineContaining(t, plain(out), needle)
		return runewidth.StringWidth(line[:strings.Index(line, needle)])
	}
	require.Equal(t, columnOf(t, named, "-193"), columnOf(t, echoed, "-193"),
		"the diff stats should not move when the glyph goes")
}

// The badge is a notification, so it has to be silent in every state that asks
// nothing of the reader -- level with the remote, or no remote at all, which is
// what an unpushed session branch and a lookup that has not returned both are.
func TestSyncBadgeSaysNothingWhenThereIsNothingToSay(t *testing.T) {
	for _, status := range []upstream.Status{
		{},
		{Ref: "origin/review"},
	} {
		plain, styled := syncBadge(status, lipgloss.NoColor{}, false)
		require.Empty(t, plain)
		require.Empty(t, styled)
	}
}

func TestSyncBadgeCountsBothDirections(t *testing.T) {
	tests := []struct {
		name   string
		status upstream.Status
		want   string
	}{
		{"behind", upstream.Status{Ref: "origin/review", Behind: 3}, upstreamBehindIcon + "3"},
		{"ahead", upstream.Status{Ref: "origin/review", Ahead: 2}, upstreamAheadIcon + "2"},
		// Both counts, each behind its own arrow, because the choice of what to do
		// about it depends on them -- and "⇅3/2" does not say which is which.
		{"diverged", upstream.Status{Ref: "origin/review", Behind: 3, Ahead: 2},
			upstreamAheadIcon + "2 " + upstreamBehindIcon + "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, styled := syncBadge(tt.status, lipgloss.NoColor{}, false)
			require.Equal(t, tt.want, plain)
			require.NotEmpty(t, styled)
		})
	}
}

// The two halves of every badge in this file have to stay in step: plain is what
// the row subtracts from the branch name's width budget, so a styled span wider
// than its plain twin overflows the pane.
func TestSyncBadgeWidthMatchesItsPlainForm(t *testing.T) {
	for _, status := range []upstream.Status{
		{Ref: "origin/review", Behind: 12},
		{Ref: "origin/review", Ahead: 4},
		{Ref: "origin/review", Behind: 12, Ahead: 4},
	} {
		plain, styled := syncBadge(status, lipgloss.NoColor{}, false)
		require.Equal(t, runewidth.StringWidth(plain), lipgloss.Width(styled))
	}
}

// A stale checkout makes everything else the row says about the branch a
// statement about the wrong commit, so the badge outlives the CI verdict as the
// pane narrows.
func TestNarrowRowKeepsTheSyncBadgeOverTheCIVerdict(t *testing.T) {
	r, inst := newTestRenderer(t, 30, "a-very-long-branch-name-that-cannot-fit", ci.Status{State: ci.StateSuccess, PRNumber: 4473})
	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/review", Behind: 3})

	line := branchLineOf(t, r.Render(inst, 1, false, false, false))

	require.Contains(t, line, upstreamBehindIcon+"3")
	require.NotContains(t, line, "#4473")
}

// Same contract as the CI badge: the row reserves the width it declares, so a
// badge must cost the line nothing. Compared against the no-badge baseline for
// the reason given on TestRenderCIBadgeDoesNotChangeLineWidth.
func TestRenderSyncBadgeDoesNotChangeLineWidth(t *testing.T) {
	const longBranch = "a-very-long-branch-name-that-cannot-possibly-fit-on-one-line"

	badged := []upstream.Status{
		{Ref: "origin/review", Behind: 1},
		{Ref: "origin/review", Ahead: 9},
		{Ref: "origin/review", Behind: 128, Ahead: 64},
	}

	for _, width := range []int{20, 30, 40, 60, 80, 120} {
		for _, ciStatus := range []ci.Status{{}, {State: ci.StateSuccess, PRNumber: 123456}} {
			base, baseInst := newTestRenderer(t, width, longBranch, ciStatus)
			baseWidth := lipgloss.Width(branchLineOf(t, base.Render(baseInst, 1, false, false, false)))

			for _, status := range badged {
				r, inst := newTestRenderer(t, width, longBranch, ciStatus)
				inst.SetUpstreamStatus(status)
				line := branchLineOf(t, r.Render(inst, 1, false, false, false))
				require.Equal(t, baseWidth, lipgloss.Width(line),
					"width %d, status %+v: badge changed the line width", width, status)
				require.Contains(t, line, branchIcon+" "+runewidth.Truncate(longBranch, 3, ""),
					"width %d, status %+v: branch name vanished entirely", width, status)
			}
		}
	}
}

// A truncated name must use every column the row can give it.
//
// runewidth.Truncate's budget already includes the tail it appends, so
// subtracting the ellipsis from the budget as well spent three columns of every
// truncated row on nothing: the name stopped three characters early and the
// line ended in blank space.
func TestTruncationSpendsTheWholeColumn(t *testing.T) {
	const width = 46

	// The longest name that still fits sets the column: a name one character
	// longer has to be cut to exactly that, ellipsis included.
	render := func(t *testing.T, title, branch string) string {
		t.Helper()
		sp := spinner.New()
		r := &InstanceRenderer{spinner: &sp}
		r.setWidth(width)
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		inst.Branch = branch
		inst.SetCurrentBranch(branch)
		return plain(r.Render(inst, 1, false, false, false))
	}

	name := func(n int) string { return "T-" + strings.Repeat("x", n) }

	// Titles: grow until one is cut, then compare against the last one that was not.
	var fits string
	for n := 1; n < 200; n++ {
		out := render(t, name(n), "b")
		if strings.Contains(out, elision) {
			require.NotEmpty(t, fits, "no title ever fit at width %d", width)
			require.Equal(t, runewidth.StringWidth(fits),
				runewidth.StringWidth(strings.TrimSpace(lineContaining(t, out, elision))),
				"a cut title should fill the column the longest uncut one did")
			break
		}
		fits = strings.TrimSpace(lineContaining(t, out, name(n)))
	}

	// Branches: same property on the second line, whose budget is computed
	// separately and made the same mistake.
	var branchFits string
	for n := 1; n < 200; n++ {
		out := render(t, "s", name(n))
		line := strings.TrimSpace(branchLineOf(t, out))
		if strings.Contains(line, elision) {
			require.NotEmpty(t, branchFits, "no branch ever fit at width %d", width)
			require.Equal(t, runewidth.StringWidth(branchFits), runewidth.StringWidth(line),
				"a cut branch should fill the column the longest uncut one did")
			break
		}
		branchFits = line
	}
}

// The row's spacing is deliberate and easy to lose to a padding tweak: a blank
// above the title and below the branch, both inside the selection bar, so the
// highlight reads as a block with its content in space rather than pressed
// against its edges.
func TestASessionKeepsItsBreathingRoom(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5901-signals", ci.Status{})
	require.Equal(t, 4, lipgloss.Height(r.Render(inst, 1, false, false, false)),
		"a blank, the title, the branch line, a blank")
	require.Equal(t, 4, lipgloss.Height(r.Render(inst, 1, true, false, false)),
		"and the selected row is the same height as the rest")

	// Which is what the list's own accounting has to see, or the window it picks
	// would be computed against a height no row has.
	l := newTestList("one", "two", "three")
	l.SetSize(80, 40)
	lines := strings.Split(plain(l.String()), "\n")
	rowOf := func(name string) int {
		for n, line := range lines {
			if strings.Contains(line, name) {
				return n
			}
		}
		t.Fatalf("no row for %q", name)
		return -1
	}
	require.Equal(t, 5, rowOf("two")-rowOf("one"),
		"five rows per session: the four it draws, plus the separator between rows")
	require.Equal(t, 5, rowOf("three")-rowOf("two"))
}

// With eight sessions on screen, which of them has been idle two minutes and
// which two days is not visible anywhere -- and it is the first thing you want
// to know when you come back to the list.
func TestAQuietRowSaysHowLongItHasBeenQuiet(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5901-signals", ci.Status{})
	inst.Status = session.Ready
	// Stamps the last-active moment, then leaves the session idle.
	inst.SetActivity(session.ActivityWorking, 0)
	inst.SetActivity(session.ActivityIdle, 0)
	inst.Acknowledge()

	at := func(d time.Duration) string {
		r.now = func() time.Time { return time.Now().Add(d) }
		return plain(lineContaining(t, r.Render(inst, 1, false, false, false), inst.Title))
	}

	require.NotContains(t, at(20*time.Second), "0m",
		"a session that stopped seconds ago has nothing to say about it")
	require.Contains(t, at(5*time.Minute), "5m")
	require.Contains(t, at(3*time.Hour), "3h")
	require.Contains(t, at(50*time.Hour), "2d")

	// A row that is still working is saying "now" with its spinner, and a number
	// beside it would be stale by the time it was read.
	inst.SetActivity(session.ActivityWorking, 0)
	require.NotContains(t, at(5*time.Minute), "5m")

	// Nothing known, nothing claimed: a session restored from a state file
	// written by an older build has no last-active time.
	fresh, err := session.NewInstance(session.InstanceOptions{Title: "restored", Path: ".", Program: "echo"})
	require.NoError(t, err)
	fresh.Status = session.Ready
	r.now = func() time.Time { return time.Now().Add(72 * time.Hour) }
	age := regexp.MustCompile(`\b\d+[mhd]\b`)
	require.NotRegexp(t, age, plain(lineContaining(t, r.Render(fresh, 1, false, false, false), fresh.Title)),
		"an unknown last-active time must not render as an age")
}

// Every branch on a ticketed repository begins the same way, so cutting the
// tail left rows distinguishable only by counting characters.
func TestALongNameIsCutInTheMiddle(t *testing.T) {
	r, inst := newTestRenderer(t, 46, "b", ci.Status{})
	require.NoError(t, inst.SetTitle("TASK-5901-signals-dedup-summary-key-rework"))

	line := plain(lineContaining(t, r.Render(inst, 1, false, false, false), elision))
	require.Contains(t, line, "TASK-5901", "the head places the name")
	require.Contains(t, line, "rework", "and the tail says which one it is")

	// The cut is inside the name, not at either end of it.
	require.NotEqual(t, elision, string([]rune(strings.TrimSpace(line))[0]))
	require.False(t, strings.HasSuffix(strings.TrimSpace(line), elision))
}

// A session that has not started yet has no branch, which left half of its row
// blank. It has a name, and the branch that name will produce follows from it.
func TestAnUnstartedRowSaysWhatItsBranchWillBe(t *testing.T) {
	render := func(t *testing.T, title string) string {
		t.Helper()
		sp := spinner.New()
		l := NewList(&sp, false)
		l.SetBranchNaming("TASK-", false)
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		l.renderer.setWidth(80)
		return plain(l.renderer.Render(inst, 1, false, false, false))
	}

	// The rules are worth showing where they change the name: spaces become
	// dashes and the prefix goes on the front, so the branch is not the title.
	require.Contains(t, render(t, "Signals Dedup"), branchIcon+" task-signals-dedup",
		"the branch the name will create belongs on the line that previews branches")

	// And not where they do not: a preview that only restates the title is held
	// to the same rule a started session's branch is.
	echoed := lineContaining(t, render(t, "TASK-5901"), notStartedNote)
	require.NotContains(t, echoed, "TASK-5901", "an echoed preview says nothing")
	require.NotContains(t, echoed, branchIcon,
		"the note is not a branch and must not wear the branch glyph")
}

// The marks are only legible if you know them, and nobody has the help screen
// open while reading the list. Where there is room, each one says what it means.
func TestBadgesSpellThemselvesOutWhenThereIsRoom(t *testing.T) {
	wide, inst := newTestRenderer(t, 90, "feature", ci.Status{State: ci.StateFailure, PRNumber: 4559, Review: ci.ReviewChangesRequested})
	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/x", Ahead: 17, Behind: 2})

	line := plain(branchLineOf(t, wide.Render(inst, 1, false, false, false)))
	require.Contains(t, line, upstreamAheadIcon+"17 "+upstreamBehindIcon+"2 diverged")
	require.Contains(t, line, ciFailureIcon+" #4559 failed")
	require.Contains(t, line, ciChangesIcon+" changes")

	// And where there is not, the words give way rather than the branch name:
	// cutting the name to make room for the word explaining a badge would be
	// paying for the explanation with the thing being explained.
	narrow, _ := newTestRenderer(t, 44, "feature", ci.Status{State: ci.StateFailure, PRNumber: 4559, Review: ci.ReviewChangesRequested})
	narrow.setWidth(44)
	line = plain(branchLineOf(t, narrow.Render(inst, 1, false, false, false)))
	require.Contains(t, line, "feature", "the branch name outranks the words")
	require.Contains(t, line, ciFailureIcon+" #4559", "the marks themselves stay")
	require.NotContains(t, line, "failed")
	require.NotContains(t, line, "diverged")
}

// A short branch name is not a squeezed one. The guard that trades the badges
// away to protect the name compared the surviving characters against a flat
// minimum, so a session on branch "zebra" read as permanently squeezed and lost
// its build verdict and its remote counts on a pane with sixty columns spare.
func TestAShortBranchNameKeepsItsBadges(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "zebra", ci.Status{State: ci.StateSuccess, PRNumber: 4561})
	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/x", Behind: 3})

	line := plain(branchLineOf(t, r.Render(inst, 1, false, false, false)))
	require.Contains(t, line, "zebra", "the name is short, not cut")
	require.Contains(t, line, "#4561")
	require.Contains(t, line, upstreamBehindIcon+"3")
}

// The window only ever scrolled DOWN: firstVisible was pulled back when the
// selection moved above it, and never when the rows below stopped filling the
// pane. Once a list had scrolled, killing a session, resizing the terminal
// taller, or just selecting the last row left sessions hidden behind "↑ N more"
// with the bottom of the pane blank.
func TestWindowScrollsBackWhenTheRowsFitAgain(t *testing.T) {
	heights := []int{5, 5, 5, 5, 5, 5}

	t.Run("everything fits, so nothing is hidden", func(t *testing.T) {
		// Where a run of `j` presses leaves it: scrolled down, selection at the end.
		l := &List{selectedIdx: 5, firstVisible: 2}
		start, end := l.window(heights, 31)
		require.Equal(t, 0, start, "all six fit in 31 rows, so the first must be drawn")
		require.Equal(t, 6, end)
	})

	t.Run("a pane that grew takes back as many as fit", func(t *testing.T) {
		// Room for four, and the selection is the last of them.
		l := &List{selectedIdx: 5, firstVisible: 4}
		start, end := l.window(heights, 20)
		require.Equal(t, 2, start, "four rows fit, so the window starts four from the end")
		require.Equal(t, 6, end)
	})

	t.Run("a row that only half fits is left out", func(t *testing.T) {
		l := &List{selectedIdx: 5, firstVisible: 5}
		start, end := l.window(heights, 12)
		require.Equal(t, 4, start, "12 rows hold two whole sessions, not two and a fifth")
		require.Equal(t, 6, end)
	})

	t.Run("scrolling down is unchanged", func(t *testing.T) {
		l := &List{selectedIdx: 5, firstVisible: 0}
		start, end := l.window(heights, 10)
		require.Equal(t, 4, start, "the selection has to be in view")
		require.Equal(t, 6, end)
	})
}

// The jump goes to a question before a finish, wherever the cursor starts, and
// the header counts them.
func TestSelectNextAttentionPrefersAQuestion(t *testing.T) {
	l := &List{renderer: &InstanceRenderer{}}
	l.SetSize(80, 40)
	var insts []*session.Instance
	for _, title := range []string{"quiet", "finished", "asking"} {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "claude"})
		require.NoError(t, err)
		l.AddInstance(inst)
		insts = append(insts, inst)
	}
	l.SetSelectedInstance(0)
	insts[1].SetActivity(session.ActivityWorking, 0)
	insts[1].SetActivity(session.ActivityIdle, 0)
	insts[2].SetActivity(session.ActivityNeedsInput, 0)

	require.Contains(t, l.attentionSummary(), "1 waiting")
	require.Contains(t, l.attentionSummary(), "1 new")

	require.True(t, l.SelectNextAttention())
	require.Equal(t, insts[2], l.GetSelectedInstance(), "the question first, though the finish is nearer")
	require.True(t, l.SelectNextAttention())
	require.Equal(t, insts[1], l.GetSelectedInstance())

	insts[2].SetActivity(session.ActivityWorking, 0) // answered
	require.False(t, l.SelectNextAttention(), "both are dealt with: the finish was seen on arrival")
	require.NotContains(t, l.attentionSummary(), "waiting")
	require.NotContains(t, l.attentionSummary(), "new")
}

// A row is un-counted from its repository only if it was counted: a naming that
// was abandoned, or a row removed before it was finalized, used to log "repo not
// found" on every kill.
func TestRepoCountFollowsRegistration(t *testing.T) {
	spin := spinner.New()
	l := NewList(&spin, false)
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", dir).Run())

	counted, err := session.NewInstance(session.InstanceOptions{Title: "counted", Path: dir, Program: "claude"})
	require.NoError(t, err)
	l.AddInstance(counted)()
	uncounted, err := session.NewInstance(session.InstanceOptions{Title: "uncounted", Path: dir, Program: "claude"})
	require.NoError(t, err)
	l.AddInstance(uncounted) // never finalized

	require.Equal(t, map[string]int{filepath.Base(dir): 1}, l.repos, "an unstarted session is still named after its repo")
	l.SetSelectedInstance(1)
	l.Remove()
	require.Equal(t, 1, l.repos[filepath.Base(dir)], "removing the uncounted row must not un-count the other")
	l.SetSelectedInstance(0)
	l.Remove()
	require.Empty(t, l.repos)
}

// A merged session has no turn to take, so it must not wear the "your turn" dot.
func TestMergedSessionIsNotReadyForYou(t *testing.T) {
	r, inst := newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{State: ci.StateMerged, PRNumber: 12})
	inst.Status = session.Ready
	require.NotContains(t, r.Render(inst, 1, false, false, false), strings.TrimSpace(readyIcon))

	r, inst = newTestRenderer(t, 80, "TASK-5823-S2S", ci.Status{State: ci.StateSuccess, PRNumber: 12})
	inst.Status = session.Ready
	require.Contains(t, r.Render(inst, 1, false, false, false), strings.TrimSpace(readyIcon))
}

// Words give way one at a time, least useful first.
func TestBadgeWordsGiveWayOneAtATime(t *testing.T) {
	status := ci.Status{State: ci.StateSuccess, PRNumber: 4773, Review: ci.ReviewApproved, Feedback: ci.FeedbackOutstanding}
	all, _ := ciBadgeAt(status, lipgloss.NoColor{}, badgeWordsAll)
	review, _ := ciBadgeAt(status, lipgloss.NoColor{}, badgeWordsReview)
	verdict, _ := ciBadgeAt(status, lipgloss.NoColor{}, badgeWordsVerdict)
	require.Contains(t, all, "comments")
	require.NotContains(t, review, "comments")
	require.Contains(t, review, "approved")
	require.NotContains(t, verdict, "approved")
	require.Contains(t, verdict, "passed")
	require.Less(t, len(verdict), len(review))
}
