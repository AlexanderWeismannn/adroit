package ui

import (
	"errors"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"github.com/AlexanderWeismannn/adroit/theme"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const readyIcon = "● "
const pausedIcon = "⏸ "

// doneChip is the mark a session wears for the few seconds after it finishes.
//
// It fills its background rather than brightening its text. theme.Success is a
// mid green in both the light and dark default palettes -- deliberately, since it
// also carries added lines and passing builds -- and a foreground bright enough
// to read as "just finished" against one theme's paper is illegible against
// another's. Reverse video is vibrant on every palette there is, and needs no
// new role.
const doneChip = " ✓ done "

// needsInputChip is the mark of a session stopped on a question for you. A chip,
// like doneChip, because this is the one state that stays until you act on it:
// a glyph in the status column is what a quiet list is made of, and this has to
// stand out of one. Warning rather than Success, since it is not finished.
const needsInputChip = " ? input "

// unseenIcon is a finished turn you have not looked at yet, once the chip has
// had its moment. Kept until the cursor lands on the row, so coming back to the
// terminal after ten minutes still says which sessions finished while you were
// away -- the ready dot alone says only that they are idle now.
const unseenIcon = "✓ "

// doneChipWindow is how long that mark lasts: long enough to catch on your way
// back to the terminal, short enough that a list left alone settles back to dots
// instead of becoming a wall of chips.
const doneChipWindow = 15 * time.Second

// spinnerLegendFrame is one frame of the animation, for the legend, which has no
// tick of its own to advance it.
const spinnerLegendFrame = "⠹"

// CI badge icons. The verdict is a glyph so it reads at a glance, and the colour
// carries the same information again for anyone scanning the column rather than
// reading the row.
const (
	ciSuccessIcon = "✓"
	ciFailureIcon = "✗"
	ciPendingIcon = "◌"
	ciMergedIcon  = "◆"
	ciClosedIcon  = "⊘"
	ciNoPRIcon    = "–"
)

// Pull-request markers, shown after the PR number. These describe the pull
// request rather than its checks: a branch can be entirely green and still
// unmergeable, and neither fact is visible in the other.
//
// All three are deliberately narrow characters that share no shape with the CI
// glyphs beside them: a heavy check for approved sat next to the light check for
// a passing build and read as one smudge, and the two circled forms that suggest
// themselves collide as well -- U+2295 with the closed U+2298, U+25C9 with the
// merged U+25C6.
//
// None may carry an emoji presentation. The obvious choice for a conflict, U+26A0
// WARNING SIGN, does in many fonts: runewidth measures it as one cell while the
// terminal draws two, silently desynchronising the width accounting the badge
// depends on. A bare "!" cannot, and U+2605 is not the emoji star (U+2B50).
const (
	ciConflictIcon = "!"
	ciChangesIcon  = "✎"
	ciApprovedIcon = "★"
	// ciCommentIcon marks review or conversation remarks. A quotation ornament:
	// it has to share no shape with the ticks, circles and diamonds beside it,
	// which is what lets the row be read by silhouette rather than by colour.
	ciCommentIcon = "❞"
)

// Upstream icons. A session's worktree is a checkout nothing updates on its own,
// so these say what has happened to the branch on the remote since: commits to
// take (behind), commits not yet pushed (ahead), or a history that has parted
// company with ours (diverged, typically an upstream force-push).
//
// Plain arrows, and not the emoji-presentation forms: like the pull-request
// markers, a glyph the terminal draws two cells wide while runewidth measures
// one desynchronises the width accounting this line depends on.
const (
	upstreamBehindIcon = "↓"
	upstreamAheadIcon  = "↑"
)

var readyStyle, addedLinesStyle, removedLinesStyle lipgloss.Style

// theme.Warning is the one CI state with no other use; success, failure and
// no-PR share their colours with the diff stats and the paused style, so the
// badge introduces no hue the list does not already use elsewhere.
var ciPendingStyle lipgloss.Style

// theme.Special: a merged pull request is neither pass nor fail, and the default
// palette gives it the violet GitHub itself uses so it reads as "landed".
var ciMergedStyle lipgloss.Style

var pausedStyle, titleStyle, listDescStyle lipgloss.Style

// workingStyle and shellStyle colour the same spinner frames differently: the
// animation says something is still happening, and the hue says which something
// -- the agent thinking, or a shell it has left running behind it.
//
// theme.Accent for the agent, so a working row belongs to the interface's own
// colour; theme.Warning for the shell, which it shares with a build still
// running, and means the same thing here.
var workingStyle, shellStyle lipgloss.Style

// doneChipStyle is reverse video by design -- see doneChip.
var doneChipStyle lipgloss.Style

// needsInputChipStyle is reverse video for the same reason as doneChipStyle;
// unseenStyle is the finished-but-unseen mark.
var needsInputChipStyle, unseenStyle lipgloss.Style

// The selected row's colours are their own pair of roles rather than a shade of
// the accent: they have to contrast with each other, which no single hue can
// guarantee across every theme.
var selectedBg, selectedFg, selectionAccent lipgloss.TerminalColor

var accentBorder = lipgloss.Border{Left: "▌"}

// Left padding drops to 0 where the border is added, so the bar occupies the
// column the padding used to and the row's total width is unchanged — the
// unselected styles keep their padding and the two stay aligned.
var selectedTitleStyle, selectedDescStyle lipgloss.Style

var mainTitle, autoYesStyle lipgloss.Style

// Styles hold a copy of their colour, so a theme change has to rebuild them
// rather than be re-read. Registered here so a package added later cannot be
// left drawing the old palette.
func init() { theme.OnChange(applyListTheme) }

func applyListTheme() {
	readyStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	addedLinesStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	removedLinesStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
	ciPendingStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Warning))
	ciMergedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Special))
	pausedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	workingStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
	shellStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Warning))
	doneChipStyle = lipgloss.NewStyle().
		Background(theme.Color(theme.Success)).
		Foreground(theme.Color(theme.AccentText)).
		Bold(true)
	needsInputChipStyle = lipgloss.NewStyle().
		Background(theme.Color(theme.Warning)).
		Foreground(theme.Color(theme.AccentText)).
		Bold(true)
	unseenStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success)).Bold(true)
	namingPlaceholderStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted)).Italic(true)
	// Muted, not Subtle: Subtle is the separator colour, #3C3C3C on the default
	// dark palette, and the age was all but invisible against the background.
	ageStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	// The caret is the one thing on the row that has to be found instantly, so it
	// takes the accent rather than the row's own foreground.
	namingCursorStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent)).Bold(true)

	// A blank row above the title and below the branch, both inside the
	// selection bar, so the highlight is a block with its content sitting in
	// space rather than jammed against its edges. Tried at two rows and at
	// three: the list fits more sessions and reads worse, and how many rows are
	// on screen is not what makes the list legible.
	titleStyle = lipgloss.NewStyle().
		Padding(1, 1, 0, 1).
		Foreground(theme.Color(theme.Text))
	listDescStyle = lipgloss.NewStyle().
		Padding(0, 1, 1, 1).
		Foreground(theme.Color(theme.Muted))

	selectedBg = theme.Color(theme.SelectionBg)
	selectedFg = theme.Color(theme.SelectionFg)
	selectionAccent = theme.Color(theme.Accent)

	selectedTitleStyle = lipgloss.NewStyle().
		Padding(1, 1, 0, 0).
		Border(accentBorder, false, false, false, true).
		BorderForeground(selectionAccent).
		BorderBackground(selectedBg).
		Background(selectedBg).
		Foreground(selectedFg)
	selectedDescStyle = lipgloss.NewStyle().
		Padding(0, 1, 1, 0).
		Border(accentBorder, false, false, false, true).
		BorderForeground(selectionAccent).
		BorderBackground(selectedBg).
		Background(selectedBg).
		Foreground(selectedFg)

	mainTitle = lipgloss.NewStyle().
		Background(theme.Color(theme.Accent)).
		Foreground(theme.Color(theme.AccentText))
	autoYesStyle = lipgloss.NewStyle().
		Background(theme.Color(theme.SelectionBg)).
		Foreground(theme.Color(theme.SelectionFg))
}

type List struct {
	items         []*session.Instance
	selectedIdx   int
	height, width int
	renderer      *InstanceRenderer
	// firstVisible is the topmost session drawn. Held rather than derived each
	// render so the list does not jump under the cursor: it moves only as far as
	// it must to keep the selection on screen.
	firstVisible int
	// naming is set while a new session's name is being typed, and branchPreview
	// is the branch that name would create.
	naming        bool
	branchPreview string
	autoyes       bool

	// map of repo name to number of instances using it. Used to display the repo name only if there are
	// multiple repos in play.
	repos map[string]int
	// repoOf is the repository each registered instance was counted under, so
	// removing it un-counts exactly what adding it counted -- including nothing,
	// for a row that never got as far as being registered.
	repoOf map[*session.Instance]string
}

func NewList(spinner *spinner.Model, autoYes bool) *List {
	return &List{
		items:    []*session.Instance{},
		renderer: &InstanceRenderer{spinner: spinner},
		repos:    make(map[string]int),
		repoOf:   make(map[*session.Instance]string),
		autoyes:  autoYes,
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	width, height = max(width, 0), max(height, 0)
	l.width = width
	l.height = height
	l.renderer.setWidth(width)
}

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	for i, item := range l.items {
		if !item.Started() || item.Paused() {
			continue
		}

		if innerErr := item.SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}

// SetBranchNaming carries the rules that turn a session's name into its branch,
// so a row can show the branch it will get before it has one.
func (l *List) SetBranchNaming(prefix string, preserveCase bool) {
	l.renderer.branchPrefix = prefix
	l.renderer.preserveBranchCase = preserveCase
}

// SetNaming marks the list as taking a new session's name, and carries the
// branch that name would produce.
func (l *List) SetNaming(naming bool, branchPreview string) {
	l.naming = naming
	l.branchPreview = branchPreview
	l.renderer.naming = naming
	l.renderer.branchPreview = branchPreview
}

// SetDevStack records which session the development stack is pointed at, and
// how it is doing, so exactly one row carries the mark.
func (l *List) SetDevStack(title string, state dev.LampState) {
	l.renderer.devStackTitle = title
	l.renderer.devStackState = state
}

func (l *List) NumInstances() int {
	return len(l.items)
}

// InstanceRenderer handles rendering of session.Instance objects
type InstanceRenderer struct {
	// devStackTitle is the session the development stack runs for, and
	// devStackState its collapsed verdict. Held here rather than on the instance
	// because the stack is a property of the machine, not of any one session:
	// exactly one row can carry the mark, and which row that is changes without
	// the instances themselves changing at all.
	devStackTitle string
	devStackState dev.LampState

	// naming and branchPreview describe the row currently being named, which is
	// always the last one.
	naming        bool
	branchPreview string

	// branchPrefix and preserveBranchCase are the rules a session's name is put
	// through to get its branch, so a row can show the branch it is going to get
	// before it has one. Held rather than looked up because the config lives in
	// app and the renderer runs every frame.
	branchPrefix       string
	preserveBranchCase bool

	spinner *spinner.Model
	width   int

	// now is the clock the just-finished window is measured against. Injectable
	// so a test can place a row inside or outside the window without sleeping
	// through it.
	now func() time.Time
}

func (r *InstanceRenderer) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

func (r *InstanceRenderer) setWidth(width int) {
	r.width = AdjustPreviewWidth(width)
}

// ɹ and ɻ are other options.
// devStackIcon marks the one session the development stack is pointed at. A
// triangle, sharing no shape with the circles, diamonds and checks beside it --
// at a glance the row is read by silhouette, not colour.
const devStackIcon = "▸"

// namingCursor is the block that marks where typing lands, and
// namingPlaceholder stands in for the name until there is one.
const (
	namingCursor      = "▏"
	namingPlaceholder = "name this session"
	// namingBranchHint fills the second line of the row while the name is still
	// empty. That line previews the branch the name will create, and with nothing
	// typed it previewed nothing -- a blank band under the cursor that read as a
	// half-drawn row rather than as a field waiting for input.
	namingBranchHint = "branch appears as you type"
	// notStartedNote stands in on a session that has no branch to preview --
	// its name already says everything the branch would.
	notStartedNote = "not started"
)

var namingPlaceholderStyle, namingCursorStyle, ageStyle lipgloss.Style

// padTo fills a cell out to width with spaces that carry the row's background.
//
// lipgloss.Place pads with plain ones, and any styled span inside the cell ends
// in a reset, so everything after it -- including that padding -- falls back to
// the terminal's own background: a dark bar running from the styled span to the
// end of an otherwise highlighted row.
func padTo(s string, width int, bg lipgloss.TerminalColor) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", pad))
}

const branchIcon = "Ꮧ"

// repoIcon marks a session running in the repository itself rather than in a
// worktree of its own. A distinct shape because the line means something
// different there: a directory, not a branch, and nothing about it is this
// session's alone.
const repoIcon = "⌂"

// minBranchWidth is the fewest characters of a branch name worth keeping. Below
// this the name stops being recognisable, so a badge that would push it there
// costs the row its identity to save its decoration.
const minBranchWidth = 6

// branchSquashed reports whether a column would cut a branch name below what is
// worth keeping.
//
// Against the name's own width as well as the threshold, because a name shorter
// than the threshold is not squashed by any column: comparing the survivors to
// a flat minBranchWidth made every branch of five characters or fewer look
// permanently squeezed, and a session on branch "zebra" silently lost its build
// verdict and its remote counts on a pane with sixty columns to spare.
func branchSquashed(branch string, column int) bool {
	need := runewidth.StringWidth(branch)
	if need > minBranchWidth {
		need = minBranchWidth
	}
	return visibleBranchWidth(branch, column) < need
}

// visibleBranchWidth reports how many characters of a branch name survive in a
// column of the given width, mirroring the truncation Render applies below.
func visibleBranchWidth(branch string, column int) int {
	width := runewidth.StringWidth(branch)
	switch {
	case column >= width:
		return width
	case column < 3:
		return 0
	default:
		return column - 3 // the ellipsis takes the rest
	}
}

// prMarker reports the one pull-request fact worth a mark next to the CI
// verdict, or "" for the ordinary case where there is nothing to say.
//
// The three are mutually exclusive and ordered by what has to happen next. A
// conflict outranks any review verdict because it blocks the merge outright and
// is the author's to fix either way; an approval is only worth showing once
// nobody is asking for changes.
//
// The label is what the mark means, spelled out. A row carrying three of these
// at once -- "! ✎ ❞" -- is a puzzle rather than a summary, and the glyphs are
// documented on a help screen nobody has open at the time.
func prMarker(status ci.Status) (icon, label string, style lipgloss.Style) {
	switch {
	case status.Mergeable == ci.MergeConflicting:
		return ciConflictIcon, "conflict", removedLinesStyle
	case status.Review == ci.ReviewChangesRequested:
		return ciChangesIcon, "changes", removedLinesStyle
	case status.Review == ci.ReviewApproved:
		return ciApprovedIcon, "approved", addedLinesStyle
	default:
		return "", "", lipgloss.Style{}
	}
}

// ciBadge renders a branch's CI verdict twice over: plain for width accounting,
// styled for display. Both are empty when there is nothing to report, which is
// also what a disabled feature and a not-yet-completed first lookup produce, so
// the badge appears only once it can say something true.
//
// With labelled set, every mark is followed by the word for what it means. The
// row is read by people who are not holding a glyph table in their heads, and
// the second line is usually mostly empty -- a session's branch is normally an
// echo of its title and dropped -- so the space is there to spend. The caller
// falls back to the bare glyphs when it is not.
//
// The two halves must stay in step: plain is what the caller subtracts from the
// branch name's width budget, so any span added to styled has to be added to
// plain too, or the row overflows its pane.
func ciBadge(status ci.Status, bg lipgloss.TerminalColor, labelled bool) (plain, styled string) {
	if labelled {
		return ciBadgeAt(status, bg, badgeWordsAll)
	}
	return ciBadgeAt(status, bg, badgeWordsNone)
}

// How many of a badge's words are spelled out. They are given up one at a time,
// least useful first, rather than all together: the row used to lose every word
// the moment the full set missed by a cell, and "✓ #4773 ★ ❞" is a glyph puzzle
// where "✓ #4773 passed ★ ❞" would have fitted.
const (
	badgeWordsNone    = 0 // glyphs only
	badgeWordsVerdict = 1 // the CI verdict's word
	badgeWordsReview  = 2 // and the review marker's
	badgeWordsAll     = 3 // and "comments"
)

func ciBadgeAt(status ci.Status, bg lipgloss.TerminalColor, words int) (plain, styled string) {
	var icon, label string
	var style lipgloss.Style

	switch status.State {
	case ci.StateSuccess:
		icon, label, style = ciSuccessIcon, "passed", addedLinesStyle
	case ci.StateFailure:
		icon, label, style = ciFailureIcon, "failed", removedLinesStyle
	case ci.StatePending:
		icon, label, style = ciPendingIcon, "running", ciPendingStyle
	// The three terminal states name themselves instead of showing the PR number.
	// Nothing is going to change about them, so there is nothing to go and look at;
	// what matters is the state, and cs documents none of its glyphs anywhere.
	// They carry no marker either: a merged pull request's review verdict is
	// history, and a closed one's is moot.
	case ci.StateMerged:
		plain = ciMergedIcon + " merged"
		return plain, ciMergedStyle.Background(bg).Render(plain)
	case ci.StateClosed:
		plain = ciClosedIcon + " closed"
		return plain, pausedStyle.Background(bg).Render(plain)
	case ci.StateNoPR:
		plain = ciNoPRIcon + " no PR"
		return plain, pausedStyle.Background(bg).Render(plain)
	default:
		return "", ""
	}

	plain = icon
	if status.PRNumber > 0 {
		plain = fmt.Sprintf("%s #%d", icon, status.PRNumber)
	}
	if words >= badgeWordsVerdict {
		plain += " " + label
	}
	styled = style.Background(bg).Render(plain)

	// The marker is a separate span because it carries its own colour: a green
	// approval next to a red cross is the whole point, and one style over both
	// would have to pick a side.
	if markerIcon, markerLabel, markerStyle := prMarker(status); markerIcon != "" {
		mark := markerIcon
		if words >= badgeWordsReview {
			mark += " " + markerLabel
		}
		plain += " " + mark
		styled += lipgloss.Style{}.Background(bg).Render(" ") +
			markerStyle.Background(bg).Render(mark)
	}

	// A second span rather than another case in prMarker, because feedback is
	// not exclusive with the rest: a pull request is routinely approved AND
	// still carrying remarks, and collapsing the two would hide one of them.
	if feedbackIcon, feedbackStyle := feedbackMarker(status); feedbackIcon != "" {
		mark := feedbackIcon
		if words >= badgeWordsAll {
			mark += " comments"
		}
		plain += " " + mark
		styled += lipgloss.Style{}.Background(bg).Render(" ") +
			feedbackStyle.Background(bg).Render(mark)
	}
	return plain, styled
}

// feedbackMarker reports whether anyone has left remarks, and how loudly to say
// so. Dim once the branch has moved past them: the remark still happened, but it
// is no longer something waiting on you.
func feedbackMarker(status ci.Status) (icon string, style lipgloss.Style) {
	switch status.Feedback {
	case ci.FeedbackOutstanding:
		return ciCommentIcon, ciPendingStyle
	case ci.FeedbackAddressed:
		return ciCommentIcon, pausedStyle
	default:
		return "", lipgloss.Style{}
	}
}

// syncBadge renders how far a session's branch has drifted from its remote
// counterpart, plain for width accounting and styled for display -- the same
// contract as ciBadge, and the same requirement that the two stay in step.
//
// Empty for a branch that is level with its remote, or has none, which is the
// ordinary state of a session doing its own work. The badge is a notification,
// so a row only grows one when there is something to do about it.
//
// Behind is warning-coloured and diverged is danger-coloured because both mean
// the code on screen is not the code on the branch. Ahead alone is dim: it is
// the normal condition of a session mid-task, and colouring it would nag about
// every unpushed commit.
func syncBadge(status upstream.Status, bg lipgloss.TerminalColor, labelled bool) (plain, styled string) {
	label := func(text, word string) string {
		if labelled {
			return text + " " + word
		}
		return text
	}

	var style lipgloss.Style
	switch {
	case !status.Tracked():
		return "", ""
	case status.Diverged():
		// Both counts, each behind its own arrow: which way to reconcile depends
		// on them, and it is the only state where updating means discarding
		// something. Written out rather than packed into one glyph and a slash --
		// "⇅2/17" does not say which number is which.
		plain = label(fmt.Sprintf("%s%d %s%d",
			upstreamAheadIcon, status.Ahead, upstreamBehindIcon, status.Behind), "diverged")
		style = removedLinesStyle
	case status.Behind > 0:
		plain = label(fmt.Sprintf("%s%d", upstreamBehindIcon, status.Behind), "behind")
		style = ciPendingStyle
	case status.Ahead > 0:
		plain = label(fmt.Sprintf("%s%d", upstreamAheadIcon, status.Ahead), "unpushed")
		style = pausedStyle
	default:
		return "", ""
	}
	return plain, style.Background(bg).Render(plain)
}

// branchEchoesTitle reports whether a branch name says nothing the title above
// it has not already said.
//
// A suffix match rather than equality, because a configured branch prefix is
// exactly the difference between the two: session "5766-Zebra-Dynamics" gets
// branch "TASK-5766-Zebra-Dynamics", and the "TASK-" is not news. Compared
// without regard to case, since lower-casing is what sanitising a branch name
// does when preserve_branch_case is off.
func branchEchoesTitle(branch, title string) bool {
	if branch == "" || title == "" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(branch), strings.ToLower(title))
}

// elision is the one-cell mark left where a name was cut.
const elision = "…"

// elideMiddle cuts a name to width by taking it out of the middle.
//
// The end of a session name is the half that identifies it: every branch on a
// ticketed repository begins "TASK-", so cutting the tail turned six rows into
// "TASK-5901-signals-de…", "TASK-5902-relationship…" -- distinguishable only by
// counting characters. The head places the name, the tail says which one it is,
// and both survive this.
func elideMiddle(s string, width int) string {
	full := runewidth.StringWidth(s)
	if width <= 0 {
		return ""
	}
	if full <= width {
		return s
	}
	mark := runewidth.StringWidth(elision)
	if width <= mark {
		return runewidth.Truncate(s, width, "")
	}
	// The head keeps the odd column: a name is read from the front, and with two
	// characters to give away the first is worth more than the last.
	room := width - mark
	for tail := room / 2; tail >= 0; tail-- {
		out := runewidth.Truncate(s, room-tail, "") + elision
		if tail > 0 {
			out += runewidth.TruncateLeft(s, full-tail, "")
		}
		// A double-width rune straddling either cut can overshoot the budget by a
		// column. Give the tail back a character rather than the whole row a
		// misaligned edge.
		if runewidth.StringWidth(out) <= width {
			return out
		}
	}
	return runewidth.Truncate(s, width, "")
}

// shortAge renders how long a session has been quiet, in the fewest characters
// that still say it. Empty under a minute: a row that changed seconds ago is
// either still spinning or wearing the just-finished chip, and "0m" on every
// other row would be noise on the one axis the list is scanned along.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return ""
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// rowAge is the quiet-for mark at the right of a row's title, and the width it
// costs -- including the space that separates it from the name.
//
// Only on a row that has stopped. A spinning row is saying "now" already, and
// the number would be a second stale by the time it was read.
func (r *InstanceRenderer) rowAge(i *session.Instance, beingNamed bool, bg lipgloss.TerminalColor) (string, int) {
	if beingNamed || i.Status == session.Loading || i.Status == session.Running {
		return "", 0
	}
	switch i.GetActivity() {
	case session.ActivityWorking, session.ActivityShell:
		return "", 0
	}
	at := i.LastActiveAt()
	if at.IsZero() {
		return "", 0
	}
	age := shortAge(r.clock().Sub(at))
	if age == "" {
		return "", 0
	}
	age = " " + age
	return ageStyle.Background(bg).Render(age), runewidth.StringWidth(age)
}

// devBadge renders the development-stack mark for a row, and the width it costs.
// The width is returned separately because the styled string carries ANSI codes
// that runewidth would count as printable.
func (r *InstanceRenderer) devBadge(i *session.Instance, bg lipgloss.TerminalColor) (string, int) {
	if r.devStackTitle == "" || r.devStackTitle != i.Title {
		return "", 0
	}
	// Shape as well as colour, the same three as the Run tab's lamps: this mark
	// used to be one glyph in three colours, which a narrow palette flattens.
	style, glyph := ciPendingStyle, lampPendingGlyph
	switch r.devStackState {
	case dev.LampUp:
		style, glyph = addedLinesStyle, lampUpGlyph
	case dev.LampDown:
		style, glyph = removedLinesStyle, lampDownGlyph
	}
	return style.Background(bg).Render(glyph), runewidth.StringWidth(glyph)
}

// statusMark is the glyph at the right-hand end of a row's title, and the width
// the row has to reserve for it.
//
// The width comes back with the string rather than being measured by the caller,
// because the mark is not always one glyph: the just-finished chip is a word, and
// like the dev-stack mark it takes its room out of the title's column so that a
// marked row stays exactly as wide as an unmarked one.
func (r *InstanceRenderer) statusMark(i *session.Instance, beingNamed, selected bool, bg lipgloss.TerminalColor) (string, int) {
	// Every mark but the chip is one glyph and a trailing space, which is the
	// width this column has always been given. Holding that constant leaves every
	// ordinary row laid out exactly as it was.
	const glyphWidth = 2

	spin := func(style lipgloss.Style) (string, int) {
		return style.Background(bg).Render(r.spinner.View() + " "), glyphWidth
	}

	switch {
	// Nothing to report about a session that does not exist yet. The Ready dot it
	// would otherwise carry belongs to the placeholder instance the name is being
	// collected into, and says something untrue about it.
	case beingNamed:
		// Blank, but painted: the column is still spent, and an unstyled hole at
		// the end of the row shows as a notch bitten out of the selection bar.
		return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", glyphWidth)), glyphWidth
	case i.Status == session.Paused:
		return pausedStyle.Background(bg).Render(pausedIcon), glyphWidth
	// Loading is the interface rebuilding the session, not the agent working, but
	// it is still something in flight -- so it spins, in the accent.
	case i.Status == session.Loading:
		return spin(workingStyle)
	}

	switch i.GetActivity() {
	case session.ActivityNeedsInput:
		return needsInputChipStyle.Render(needsInputChip), runewidth.StringWidth(needsInputChip)
	case session.ActivityWorking:
		return spin(workingStyle)
	case session.ActivityShell:
		// Animated even though the agent has stopped. Something it started is
		// still running and it will pick the work back up by itself, so a still
		// mark here is precisely the wrong answer -- that is the whole point of
		// this state. Amber rather than the accent says which of the two it is.
		return spin(shellStyle)
	}

	// Activity is only read off interfaces we can parse. For every other program
	// the pane-content hash still drives Status, so Running there is all we know
	// and it still means the spinner.
	if i.Status == session.Running {
		return spin(workingStyle)
	}

	if done := i.DoneAt(); !done.IsZero() {
		if r.clock().Sub(done) < doneChipWindow {
			return doneChipStyle.Render(doneChip), runewidth.StringWidth(doneChip)
		}
		// The row under the cursor is on screen in the preview, so there is
		// nothing about it left unseen.
		if !selected {
			return unseenStyle.Background(bg).Render(unseenIcon), glyphWidth
		}
	}

	// The ready dot means "your turn". On a session whose pull request is
	// already merged or closed there is no turn to take -- the only thing left
	// to do with it is kill it -- so it carries no dot.
	if i.Status == session.Ready && !prSettled(i) {
		return readyStyle.Background(bg).Render(readyIcon), glyphWidth
	}
	return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", glyphWidth)), glyphWidth
}

// prSettled reports whether the session's pull request is merged or closed.
func prSettled(i *session.Instance) bool {
	st := i.GetCIStatus().State
	return st == ci.StateMerged || st == ci.StateClosed
}

// settled reports whether a session's work is over for now: its pull request
// is merged or closed, or it is parked. Such a row is drawn muted.
func settled(i *session.Instance) bool {
	return i.Status == session.Paused || prSettled(i)
}

func (r *InstanceRenderer) Render(i *session.Instance, idx int, selected bool, hasMultipleRepos bool, isLast bool) string {
	prefix := fmt.Sprintf(" %d. ", idx)
	if idx >= 10 {
		prefix = prefix[:len(prefix)-1]
	}
	titleS := selectedTitleStyle
	descS := selectedDescStyle
	if !selected {
		titleS = titleStyle
		descS = listDescStyle
		// A row whose work is over -- the pull request merged or closed, or the
		// session parked -- recedes, so the live rows are what the eye lands on.
		// The badge still says which.
		if settled(i) {
			titleS = titleS.Foreground(theme.Color(theme.Muted))
		}
	}

	beingNamed := r.naming && isLast
	join, joinWidth := r.statusMark(i, beingNamed, selected, titleS.GetBackground())
	// How long the row has been quiet, taken out of the title's column like every
	// other mark on the line.
	age, ageWidth := r.rowAge(i, beingNamed, titleS.GetBackground())

	// A session being named has to look like it is being named.
	//
	// It used to render as a bare number and an empty branch glyph: you typed
	// into a row that showed nothing back, with the footer as the only sign
	// anything was happening. A cursor says where the text is going, and the
	// placeholder says what is wanted.
	titleText := i.Title
	// The placeholder is styled after the width accounting below rather than
	// before it: runewidth counts an escape sequence as printable text, so a
	// pre-styled string measures at several times its width here and a
	// truncation cuts through the middle of a colour code.
	showPlaceholder := beingNamed && titleText == ""
	if showPlaceholder {
		titleText = namingPlaceholder
	}

	// Cut the title if it's too long
	_, devMarkBudget := r.devBadge(i, titleS.GetBackground())
	widthAvail := r.width - 1 - joinWidth - devMarkBudget - ageWidth - runewidth.StringWidth(prefix) - 1
	if beingNamed {
		// The cursor sits after the text and has to fit beside it.
		widthAvail -= runewidth.StringWidth(namingCursor)
	}
	if widthAvail > 0 && runewidth.StringWidth(titleText) > widthAvail {
		titleText = elideMiddle(titleText, widthAvail)
	}
	if showPlaceholder {
		titleText = namingPlaceholderStyle.Background(titleS.GetBackground()).Render(titleText)
	}
	if beingNamed {
		titleText += namingCursorStyle.Background(titleS.GetBackground()).Render(namingCursor)
	}
	// The stack mark takes its width out of the title's column rather than adding
	// to the row, so a marked row stays exactly as wide as an unmarked one.
	devMark, devMarkWidth := r.devBadge(i, titleS.GetBackground())
	// The separator has to carry the row's background like the segments either
	// side of it do. Each of those is rendered with its own style, and lipgloss
	// closes every style with a reset, so a plain " " joined between two of them
	// falls back to the terminal's own background -- a dark notch punched through
	// the middle of a selected row's bar. Invisible until something is drawn on
	// both sides of it, which is why it surfaced only once the status mark started
	// setting a background of its own.
	gap := lipgloss.NewStyle().Background(titleS.GetBackground()).Render(" ")
	title := titleS.Render(lipgloss.JoinHorizontal(
		lipgloss.Left,
		padTo(fmt.Sprintf("%s %s", prefix, titleText), r.width-1-joinWidth-devMarkWidth-ageWidth, titleS.GetBackground()),
		age,
		devMark,
		gap,
		join,
	))

	stat := i.GetDiffStats()

	var diff string
	var addedDiff, removedDiff string
	if stat == nil || stat.Error != nil || stat.IsEmpty() {
		// Don't show diff stats if there's an error or if they don't exist
		addedDiff = ""
		removedDiff = ""
		diff = ""
	} else {
		// Compact: "+25064 -2387" cost the row enough width that the badge
		// beside it lost every one of its words; "+25k -2.4k" pays for them.
		addedDiff = "+" + compactCount(stat.Added)
		removedDiff = "-" + compactCount(stat.Removed) + " "
		// A space rather than a comma: the two counts are separate quantities, not
		// a list, and "+2200,-12" reads as one token. Same width either way, so the
		// accounting below is unaffected.
		diff = lipgloss.JoinHorizontal(
			lipgloss.Center,
			addedLinesStyle.Background(descS.GetBackground()).Render(addedDiff),
			lipgloss.Style{}.Background(descS.GetBackground()).Foreground(descS.GetForeground()).Render(" "),
			removedLinesStyle.Background(descS.GetBackground()).Render(removedDiff),
		)
	}

	remainingWidth := r.width
	remainingWidth -= runewidth.StringWidth(prefix)
	remainingWidth -= runewidth.StringWidth(branchIcon) // same width as repoIcon
	remainingWidth -= 2                                 // for the literal " " and "-" in the branchLine format string

	diffWidth := runewidth.StringWidth(addedDiff) + runewidth.StringWidth(removedDiff)
	if diffWidth > 0 {
		diffWidth += 1
	}

	// Use fixed width for diff stats to avoid layout issues
	remainingWidth -= diffWidth

	// The branch the worktree actually has checked out, not the one the session
	// was created with — see Instance.DisplayBranch.
	branch := i.DisplayBranch()
	rowIcon := branchIcon
	if i.NoWorktree() {
		// No branch to name. The directory it runs in is the useful fact, and
		// naming the repository's current HEAD here would claim a branch that is
		// not this session's and can change under it.
		rowIcon = repoIcon
		branch = filepath.Base(i.Path)
	}
	// A branch that only restates the title is worse than nothing there.
	//
	// A session names its branch after itself, so in the ordinary case this line
	// read "Ꮧ TASK-5..." beneath a title that already said
	// "TASK-5633-Remove-Old-RBAC" -- the row's own name, truncated past the point
	// of meaning. And the width it took came out of the pull-request badge's
	// budget, which the guard below drops FIRST when space is short: the half
	// that told you something was the half that disappeared.
	branchIsEcho := branchEchoesTitle(branch, i.Title)
	if branchIsEcho {
		branch = ""
	}
	// While naming, the second line previews the branch the name will create --
	// the one place the prefix and sanitising rules are visible before they have
	// already been applied to a branch you are stuck with.
	// An unstarted session has no branch to name, which left its second line
	// blank -- half the row saying nothing. It has a name, though, and the branch that name will produce is derivable
	// from it, so the line says what is coming instead of nothing at all.
	// Only a session that has no branch at all: one whose name was dropped for
	// echoing the title has a branch, and saying "not started" about it would be
	// a statement about the wrong thing. Nor is a paused session unstarted -- it
	// has run, and its own mark already says where it stands.
	pendingBranch := false
	if !beingNamed && branch == "" && !branchIsEcho && !i.Started() && !i.Paused() &&
		i.Status != session.Paused && !i.NoWorktree() {
		preview := git.PreviewBranchName(r.branchPrefix, r.preserveBranchCase, i.Title)
		// An echo is no more worth showing here than it is on a started session.
		if preview != "" && !branchEchoesTitle(preview, i.Title) {
			branch, pendingBranch = preview, true
			rowIcon = branchIcon
		} else {
			// Not a branch, so not the branch glyph: the note labels the session's
			// state, and the width the icon would have taken stays spent so the
			// line starts in the same column as every other row's.
			branch, pendingBranch = notStartedNote, true
			rowIcon = strings.Repeat(" ", runewidth.StringWidth(rowIcon))
		}
		branchIsEcho = false
	}

	showBranchHint := false
	if beingNamed {
		rowIcon = branchIcon
		branch = r.branchPreview
		branchIsEcho = false
		// Nothing typed yet, so there is no branch to preview. Say what the line
		// is for instead of leaving it blank -- styled at the end, for the same
		// reason the title placeholder is.
		if branch == "" {
			branch = namingBranchHint
			showBranchHint = true
		}
	}

	if i.Started() && hasMultipleRepos {
		repoName, err := i.RepoName()
		if err != nil {
			log.ErrorLog.Printf("could not get repo name in instance renderer: %v", err)
		} else if branch == "" {
			branch = repoName
		} else {
			branch += fmt.Sprintf(" (%s)", repoName)
		}
	}
	// Reserve the badges out of the width budget before the branch is truncated,
	// so an overlong branch gives way to the verdict rather than pushing it off the
	// end of the line.
	//
	// Built twice: once with every mark spelled out, and once as bare glyphs. The
	// words are what make the line readable without a legend, and the line is
	// usually mostly empty -- a branch that echoes its title is dropped, and most
	// do -- so on any ordinary row they fit. Where they do not, the glyphs are
	// what the space is for: a name cut short to make room for the word "failed"
	// would be paying for the explanation with the thing being explained.
	badges := func(words int) (ciStyled string, ciWidth int, syncStyled string, syncWidth int) {
		ciPlain, ciStyled := ciBadgeAt(i.GetCIStatus(), descS.GetBackground(), words)
		ciWidth = runewidth.StringWidth(ciPlain)
		if ciWidth > 0 {
			ciWidth += 2 // the spaces either side of the badge in branchLine
		}
		// The upstream badge is reserved the same way, and separately, because it
		// outranks the verdict when neither fits: a stale checkout makes every
		// other thing the row says about the branch a statement about the wrong
		// commit.
		// Its word goes last of all, with the verdict's: it is the one that says
		// the rest of the line describes the wrong commit.
		syncPlain, syncStyled := syncBadge(i.GetUpstreamStatus(), descS.GetBackground(), words >= badgeWordsVerdict)
		syncWidth = runewidth.StringWidth(syncPlain)
		if syncWidth > 0 {
			syncWidth++ // the space before the badge in branchLine
		}
		return ciStyled, ciWidth, syncStyled, syncWidth
	}

	// The words are affordable only when nothing has to be cut to fit them.
	ciStyled, ciWidth, syncStyled, syncWidth := badges(badgeWordsAll)
	for words := badgeWordsAll - 1; words >= badgeWordsNone &&
		runewidth.StringWidth(branch)+ciWidth+syncWidth > remainingWidth; words-- {
		ciStyled, ciWidth, syncStyled, syncWidth = badges(words)
	}
	// Up to a point: the branch name is the row's identity and the badge is an
	// extra, so on a pane too narrow for both it is the badge that gives way.
	// Otherwise a 20-column list renders an icon, an ellipsis and no name at all.
	// With no branch to protect there is nothing to trade against, and the badge
	// keeps the whole line -- which is the point of dropping the echo above.
	// The guard protects a branch NAME. A repo label standing in for one is not
	// that, and a short label would otherwise drop the badge for nothing.
	if !branchIsEcho && branch != "" && branchSquashed(branch, remainingWidth-ciWidth-syncWidth) {
		ciStyled, ciWidth = "", 0
		// Still no room with the verdict gone, so the branch takes the line back.
		if branchSquashed(branch, remainingWidth-syncWidth) {
			syncStyled, syncWidth = "", 0
		}
	}
	remainingWidth -= ciWidth + syncWidth

	// Don't show branch if there's no space for it. Or show ellipsis if it's too long.
	branchWidth := runewidth.StringWidth(branch)
	if remainingWidth < 0 {
		branch = ""
	} else if remainingWidth < branchWidth {
		if remainingWidth < 3 {
			branch = ""
		} else {
			branch = elideMiddle(branch, remainingWidth)
		}
	}
	remainingWidth -= runewidth.StringWidth(branch)

	// Add spaces to fill the remaining width. Painted, because the hint below may
	// have put a style -- and so a reset -- immediately before them.
	spaces := ""
	if remainingWidth > 0 {
		spaces = lipgloss.NewStyle().Background(descS.GetBackground()).Render(strings.Repeat(" ", remainingWidth))
	}

	// Styled only now that its width has been counted and any truncation applied.
	if pendingBranch && branch != "" {
		branch = namingPlaceholderStyle.Background(descS.GetBackground()).Render(branch)
	}
	if showBranchHint && branch != "" {
		branch = namingPlaceholderStyle.Background(descS.GetBackground()).Render(branch)
	}

	syncSegment := ""
	if syncStyled != "" {
		syncSegment = lipgloss.Style{}.Background(descS.GetBackground()).Render(" ") + syncStyled
	}

	ciSegment := ""
	if ciStyled != "" {
		// The padding spaces have to carry the row background themselves. They sit
		// either side of an already-styled span whose trailing reset clears it, so a
		// bare " " here renders as a black gap on the selected row rather than picking
		// up its highlight. Same reason the diff separator below is styled.
		pad := lipgloss.Style{}.Background(descS.GetBackground()).Render(" ")
		ciSegment = pad + ciStyled + pad
	}

	// No name, so no glyph. It exists to label a branch or a directory, and with
	// the name gone -- an echo of the title, or a pane too narrow to fit it -- it
	// labels an empty space, where it reads as a stray mark rather than as an icon.
	//
	// Its width stays spent rather than reclaimed, so the CI badge and the diff
	// stats sit in the same column as they do on every other row. Reclaiming it
	// would make a row shift sideways for no reason the eye can attribute.
	if branch == "" {
		rowIcon = strings.Repeat(" ", runewidth.StringWidth(rowIcon))
	}

	// A space after the branch glyph, not a hyphen: "Ꮧ-TASK-5636" reads as though
	// the hyphen were part of the branch name. Still two literal characters, so
	// the width reserved for them above is unchanged.
	branchLine := fmt.Sprintf("%s %s %s%s%s%s%s", strings.Repeat(" ", len(prefix)), rowIcon, branch, spaces, syncSegment, ciSegment, diff)

	// join title and subtitle
	text := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		descS.Render(branchLine),
	)

	return text
}

// scrollIndicator styles the "N more" markers that stand in for the rows a pane
// too short to hold them cannot show.
func scrollIndicator(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return pausedStyle.Render(fmt.Sprintf("   %s %d more", arrow, n))
}

// window picks the run of sessions to draw, keeping the selection in view.
//
// Without one the list drew every session and let the surplus fall off the
// bottom of the terminal: twelve sessions in a forty-row window rendered
// sixty-eight rows, and the five that did not fit were simply unreachable --
// no scroll, and nothing to say they existed.
//
// It scrolls by whole sessions rather than by rows. A part-drawn session at the
// edge would be a title with no status, or a status line belonging to a name
// you cannot see.
func (l *List) window(heights []int, budget int) (start, end int) {
	if len(heights) == 0 {
		return 0, 0
	}
	if l.firstVisible > l.selectedIdx {
		l.firstVisible = l.selectedIdx
	}
	if l.firstVisible >= len(heights) {
		l.firstVisible = len(heights) - 1
	}
	if l.firstVisible < 0 {
		l.firstVisible = 0
	}

	for {
		used, last := 0, l.firstVisible
		for last < len(heights) && used+heights[last] <= budget {
			used += heights[last]
			last++
		}
		// Always draw at least the selected session, even in a pane too short for
		// it: a blank list would be a worse answer than a clipped one.
		if last == l.firstVisible {
			last = l.firstVisible + 1
		}
		if last > l.selectedIdx || l.firstVisible >= len(heights)-1 {
			return l.reclaim(heights, budget, last)
		}
		l.firstVisible++
	}
}

// reclaim pulls the top of the window back over sessions that now fit.
//
// Scrolling DOWN is driven by the selection, and nothing drove the other
// direction: firstVisible stayed where the descent had left it. Killing a
// session, resizing the terminal taller, or simply selecting the last row then
// left the list drawing four sessions of six, reporting "↑ 2 more", and leaving
// the bottom third of the pane blank -- room the hidden sessions would have
// fitted into twice over.
//
// Only rows that fit ENTIRELY are taken back, and end is left where the caller
// put it: the sessions already on screen keep their place, and one of them
// scrolling off to make room for one above would be a worse answer than the gap.
func (l *List) reclaim(heights []int, budget, last int) (start, end int) {
	used := 0
	for i := l.firstVisible; i < last; i++ {
		used += heights[i]
	}
	for l.firstVisible > 0 && used+heights[l.firstVisible-1] <= budget {
		l.firstVisible--
		used += heights[l.firstVisible]
	}
	return l.firstVisible, last
}

func (l *List) String() string {
	const autoYesText = " auto-yes "

	// The count is otherwise only obtainable by counting rows. Omitted at zero,
	// where "· 0" says less than the empty-state message in the preview pane.
	titleText := " Instances "
	if len(l.items) > 0 {
		titleText = fmt.Sprintf(" Instances · %d ", len(l.items))
	}

	titleLine := mainTitle.Render(titleText) + l.attentionSummary()

	// Write the title.
	//
	// One blank row above it, not two. The pane beside it leads with the same
	// gap, so they were both spending a row of every screen on the same piece of
	// nothing; trimmed together they stay level, and the row goes to a session.
	var b strings.Builder
	b.WriteString("\n")

	// Write title line
	// add padding of 2 because the border on list items adds some extra characters
	//
	// Clipped to that width, because lipgloss.Place does not: given less room
	// than its content it hands the content back whole. The rows below elide
	// themselves, so in a narrow terminal this line alone was wider than the
	// column it heads -- " Instances · 4 " is 15 cells against the 12 the list
	// gets at 40 columns -- and the pane beside it was pushed off the screen.
	titleWidth := AdjustPreviewWidth(l.width) + 2
	if !l.autoyes {
		b.WriteString(lipgloss.Place(
			titleWidth, 1, lipgloss.Left, lipgloss.Bottom, clip(titleLine, titleWidth)))
	} else {
		half := titleWidth / 2
		title := lipgloss.Place(
			half, 1, lipgloss.Left, lipgloss.Bottom, clip(titleLine, half))
		autoYes := lipgloss.Place(
			titleWidth-half, 1, lipgloss.Right, lipgloss.Bottom,
			clip(autoYesStyle.Render(autoYesText), titleWidth-half))
		b.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top, title, autoYes))
	}

	b.WriteString("\n")
	b.WriteString("\n")

	header := b.String()
	rendered := make([]string, len(l.items))
	heights := make([]int, len(l.items))
	total := 0
	for i, item := range l.items {
		rendered[i] = l.renderer.Render(item, i+1, i == l.selectedIdx, len(l.repos) > 1, i == len(l.items)-1)
		// The blank line that separates one session from the next belongs to the
		// session above it, so a window's height is the sum of what it draws.
		heights[i] = strings.Count(rendered[i], "\n") + 2
		total += heights[i]
	}

	budget := l.height - (strings.Count(header, "\n") + 1)
	// Two rows held back for the markers, but only when something is going to be
	// hidden -- otherwise a list that fits exactly would be scrolled for nothing.
	if total > budget {
		budget -= 2
	}

	start, end := l.window(heights, budget)
	if start > 0 {
		b.WriteString(scrollIndicator("↑", start) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(rendered[i])
		if i != end-1 {
			b.WriteString("\n\n")
		}
	}
	if end < len(l.items) {
		b.WriteString("\n" + scrollIndicator("↓", len(l.items)-end))
	}

	// Nothing may be wider than the column the list was given. lipgloss.Place
	// pads to that width but never cuts to it, and a row that overruns pushes the
	// pane beside it off the screen -- the whole frame then renders wider than the
	// terminal and tmux clips its right-hand edge. The row's own accounting elides
	// the name long before this; it is the backstop for the narrow widths where
	// there is no room left to elide into.
	lines := strings.Split(b.String(), "\n")
	for i, line := range lines {
		lines[i] = clip(line, l.width)
	}

	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, strings.Join(lines, "\n"))
}

// wantsAttention ranks what a session is asking of you: 2 for a question it
// cannot go on without, 1 for a finish you have not looked at, 0 for nothing.
// The selected row is never unseen -- it is in the preview.
func (l *List) wantsAttention(idx int) int {
	inst := l.items[idx]
	switch {
	case inst.Status == session.Paused:
		return 0
	case inst.GetActivity() == session.ActivityNeedsInput:
		return 2
	case !inst.DoneAt().IsZero() && idx != l.selectedIdx:
		return 1
	}
	return 0
}

// attentionSummary is the header's tally of what the rows below are asking of
// you, so the answer to "does anything need me?" does not take reading every
// row. Only the states that exist are listed; a list with none says nothing.
func (l *List) attentionSummary() string {
	var input, unseen, working int
	for idx, inst := range l.items {
		switch l.wantsAttention(idx) {
		case 2:
			input++
			continue
		case 1:
			unseen++
			continue
		}
		if inst.Status == session.Paused {
			continue
		}
		if a := inst.GetActivity(); a == session.ActivityWorking || a == session.ActivityShell ||
			inst.Status == session.Running || inst.Status == session.Loading {
			working++
		}
	}
	var parts []string
	if input > 0 {
		parts = append(parts, ciPendingStyle.Bold(true).Render(fmt.Sprintf("? %d waiting", input)))
	}
	if unseen > 0 {
		parts = append(parts, unseenStyle.Render(fmt.Sprintf("%s%d new", unseenIcon, unseen)))
	}
	if working > 0 {
		parts = append(parts, workingStyle.Render(fmt.Sprintf("%s %d working", spinnerLegendFrame, working)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, pausedStyle.Render(" · "))
}

// HasAttention reports whether any session is waiting on you or has a finish
// you have not seen.
func (l *List) HasAttention() bool {
	for idx := range l.items {
		if l.wantsAttention(idx) > 0 {
			return true
		}
	}
	return false
}

// SelectNextAttention moves the cursor to the next OTHER session that wants you
// -- one waiting on an answer before one that merely finished -- searching
// forward from the cursor and wrapping. The row under the cursor is skipped, so
// pressing the key again moves on rather than staying put. Reports whether there
// was one to go to.
func (l *List) SelectNextAttention() bool {
	n := len(l.items)
	for _, rank := range []int{2, 1} {
		for step := 1; step < n; step++ {
			idx := (l.selectedIdx + step) % n
			if l.wantsAttention(idx) == rank {
				l.SetSelectedInstance(idx)
				return true
			}
		}
	}
	return false
}

// acknowledgeSelected clears the just-finished mark on the row under the cursor.
// Called both on the row the cursor lands on and on the one it leaves: a row
// that finished while it was selected was in the preview the whole time, and
// must not turn up as unseen the moment you move off it.
//
// Called where the cursor lands rather than from the render, which would clear
// the mark on the row you happen to have parked on and never show it at all.
func (l *List) acknowledgeSelected() {
	// Bounds-checked here rather than trusted: Remove moves the cursor up after
	// the row under it is gone, so the row being "left" can be one that no
	// longer exists.
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return
	}
	l.items[l.selectedIdx].Acknowledge()
}

// Down selects the next item in the list.
func (l *List) Down() {
	if len(l.items) == 0 {
		return
	}
	l.acknowledgeSelected() // the row being left was on screen until now
	if l.selectedIdx < len(l.items)-1 {
		l.selectedIdx++
	} else {
		l.selectedIdx = 0
	}
	l.acknowledgeSelected()
}

// Remove takes the selected session out of the list and hands it back, leaving
// its tmux session and its worktree standing.
//
// This is the half of a kill that a caller on the update loop can afford. The
// other half -- Instance.Kill -- shells out to tmux and to git, and neither is
// quick when another process holds a lock, so it belongs on a Cmd. Splitting
// them lets the row disappear on the keypress while the teardown runs behind
// it. Remove returns nil when there is nothing selected to remove.
func (l *List) Remove() *session.Instance {
	if len(l.items) == 0 {
		return nil
	}
	targetInstance := l.items[l.selectedIdx]

	// Before the worktree goes: both lookups are cached on its path, and a killed
	// session is the one moment either can be told the path will never be asked
	// about again.
	if worktree, err := targetInstance.GetGitWorktree(); err == nil {
		ci.Forget(worktree.GetRepoPath(), worktree.GetWorktreePath())
		upstream.Forget(worktree.GetWorktreePath())
	}

	// If you delete the last one in the list, select the previous one.
	if l.selectedIdx == len(l.items)-1 {
		defer l.Up()
	}

	// Unregister the reponame, if it was ever registered.
	if repoName, ok := l.repoOf[targetInstance]; ok {
		delete(l.repoOf, targetInstance)
		l.rmRepo(repoName)
	}

	// Since there's items after this, the selectedIdx can stay the same.
	l.items = append(l.items[:l.selectedIdx], l.items[l.selectedIdx+1:]...)
	return targetInstance
}

// Kill removes the selected session and tears it down inline. Anything running
// on the update loop wants Remove and a Cmd instead.
func (l *List) Kill() {
	targetInstance := l.Remove()
	if targetInstance == nil {
		return
	}

	if err := targetInstance.Kill(); err != nil {
		log.ErrorLog.Printf("could not kill instance: %v", err)
	}
}

func (l *List) Attach() (chan struct{}, error) {
	targetInstance := l.items[l.selectedIdx]
	targetInstance.Acknowledge()
	return targetInstance.Attach()
}

// Up selects the prev item in the list.
func (l *List) Up() {
	if len(l.items) == 0 {
		return
	}
	l.acknowledgeSelected() // the row being left was on screen until now
	if l.selectedIdx > 0 {
		l.selectedIdx--
	} else {
		l.selectedIdx = len(l.items) - 1
	}
	l.acknowledgeSelected()
}

func (l *List) addRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		l.repos[repo] = 0
	}
	l.repos[repo]++
}

func (l *List) rmRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		log.ErrorLog.Printf("repo %s not found", repo)
		return
	}
	l.repos[repo]--
	if l.repos[repo] == 0 {
		delete(l.repos, repo)
	}
}

// AddInstance adds a new instance to the list. It returns a finalizer function that should be called when the instance
// is started. If the instance was restored from storage or is paused, you can call the finalizer immediately.
// When creating a new one and entering the name, you want to call the finalizer once the name is done.
func (l *List) AddInstance(instance *session.Instance) (finalize func()) {
	l.items = append(l.items, instance)
	// The finalizer registers the repo name once the instance is started.
	return func() {
		if _, done := l.repoOf[instance]; done {
			return
		}
		repoName, err := instance.RepoName()
		if err != nil {
			log.ErrorLog.Printf("could not get repo name: %v", err)
			return
		}
		if l.repoOf == nil {
			l.repoOf = make(map[*session.Instance]string)
		}
		if l.repos == nil {
			l.repos = make(map[string]int)
		}
		l.repoOf[instance] = repoName
		l.addRepo(repoName)
	}
}

// GetSelectedInstance returns the currently selected instance
func (l *List) GetSelectedInstance() *session.Instance {
	if len(l.items) == 0 {
		return nil
	}
	return l.items[l.selectedIdx]
}

// SetSelectedInstance sets the selected index. Noop if the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx >= len(l.items) {
		return
	}
	l.acknowledgeSelected() // the row being left was on screen until now
	l.selectedIdx = idx
	l.acknowledgeSelected()
}

// SelectInstance finds and selects the given instance in the list.
func (l *List) SelectInstance(target *session.Instance) {
	for i, inst := range l.items {
		if inst == target {
			l.SetSelectedInstance(i)
			return
		}
	}
}

// MoveUp swaps the selected instance with the one above it.
func (l *List) MoveUp() bool {
	if l.selectedIdx <= 0 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx-1] = l.items[l.selectedIdx-1], l.items[l.selectedIdx]
	l.selectedIdx--
	return true
}

// MoveDown swaps the selected instance with the one below it.
func (l *List) MoveDown() bool {
	if l.selectedIdx >= len(l.items)-1 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx+1] = l.items[l.selectedIdx+1], l.items[l.selectedIdx]
	l.selectedIdx++
	return true
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items
}

// BadgeLegend explains the glyph vocabulary used in the instance list.
//
// It lives here, next to the glyphs and styles it describes, and is exported for
// the help screen rather than re-spelled there: a legend that names its own
// colours would drift from the rows it documents the first time one of them
// changed.
func BadgeLegend() []string {
	dim := pausedStyle
	pair := func(style lipgloss.Style, icon, label string) string {
		return style.Render(icon) + dim.Render("  "+label)
	}
	sep := dim.Render("    ")

	return []string{
		strings.Join([]string{
			pair(readyStyle, strings.TrimSpace(readyIcon), "ready"),
			pair(pausedStyle, strings.TrimSpace(pausedIcon), "paused"),
		}, sep),
		strings.Join([]string{
			// One frame of the animation, twice. The two states differ only in
			// colour, which is exactly the thing a legend is for.
			pair(workingStyle, spinnerLegendFrame, "agent working"),
			pair(shellStyle, spinnerLegendFrame, "shell still running"),
		}, sep),
		strings.Join([]string{
			doneChipStyle.Render(doneChip) + dim.Render("  just finished"),
			pair(unseenStyle, strings.TrimSpace(unseenIcon), "finished, not looked at yet"),
		}, sep),
		needsInputChipStyle.Render(needsInputChip) + dim.Render("  waiting on your answer"),
		strings.Join([]string{
			pair(pausedStyle, branchIcon, "branch in its own worktree"),
			pair(pausedStyle, repoIcon, "runs in the repo itself"),
		}, sep),
		strings.Join([]string{
			pair(addedLinesStyle, lampUpGlyph, "dev stack up here"),
			pair(ciPendingStyle, lampPendingGlyph, "starting"),
			pair(removedLinesStyle, lampDownGlyph, "down"),
			pair(ageStyle, "2h", "quiet for that long"),
		}, sep),
		strings.Join([]string{
			pair(addedLinesStyle, ciSuccessIcon, "CI passed"),
			pair(removedLinesStyle, ciFailureIcon, "CI failed"),
			pair(ciPendingStyle, ciPendingIcon, "CI running"),
		}, sep),
		strings.Join([]string{
			pair(ciMergedStyle, ciMergedIcon, "merged"),
			pair(pausedStyle, ciClosedIcon, "closed"),
			pair(pausedStyle, ciNoPRIcon, "no PR"),
		}, sep),
		strings.Join([]string{
			pair(ciPendingStyle, upstreamBehindIcon, "behind the remote"),
			pair(pausedStyle, upstreamAheadIcon, "commits not pushed"),
			// Diverged is not a glyph of its own any more: it is both counts, each
			// behind its own arrow, which is the only form that says which is which.
			pair(removedLinesStyle, upstreamAheadIcon+"n "+upstreamBehindIcon+"n", "diverged from it"),
		}, sep),
		strings.Join([]string{
			pair(addedLinesStyle, ciApprovedIcon, "approved"),
			pair(removedLinesStyle, ciChangesIcon, "changes requested"),
			pair(removedLinesStyle, ciConflictIcon, "merge conflict"),
			pair(ciPendingStyle, ciCommentIcon, "comments"),
		}, sep),
	}
}
