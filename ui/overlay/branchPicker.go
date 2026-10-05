package overlay

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const newBranchOption = "New branch (from HEAD)"

// noWorktreeOption starts a session in the repository as it stands, with no
// worktree and no branch of its own -- a scratch terminal on the work already in
// progress, rather than an isolated place to do new work.
//
// Matched by string, which is safe because a git ref name cannot contain a space:
// no real branch can ever collide with this label or with newBranchOption.
const noWorktreeOption = "No branch (run in the repo itself)"

// BranchPicker is an embeddable component for selecting a branch.
// It does not hold the full branch list — results are provided asynchronously
// via SetResults after each debounced search.
type BranchPicker struct {
	results       []string // current search results (from git)
	filter        string   // current filter text
	filterVersion uint64   // incremented on each filter change
	cursor        int      // index into visibleItems()
	focused       bool
	width         int
	showNewBranch bool // whether to show the "New branch" option
}

// NewBranchPicker creates a new empty branch picker.
func NewBranchPicker() *BranchPicker {
	return &BranchPicker{
		showNewBranch: true,
	}
}

// SetWidth sets the width of the branch picker.
func (bp *BranchPicker) SetWidth(w int) {
	bp.width = w
}

// Focus gives the branch picker focus.
func (bp *BranchPicker) Focus() {
	bp.focused = true
}

// Blur removes focus from the branch picker.
func (bp *BranchPicker) Blur() {
	bp.focused = false
}

// IsFocused returns whether the branch picker is focused.
func (bp *BranchPicker) IsFocused() bool {
	return bp.focused
}

// GetFilter returns the current filter text.
func (bp *BranchPicker) GetFilter() string {
	return bp.filter
}

// GetFilterVersion returns a monotonically increasing version that changes on every filter edit.
func (bp *BranchPicker) GetFilterVersion() uint64 {
	return bp.filterVersion
}

// HandleKeyPress processes a key event. Returns (consumed, filterChanged).
func (bp *BranchPicker) HandleKeyPress(msg tea.KeyMsg) (consumed bool, filterChanged bool) {
	switch msg.Type {
	case tea.KeyUp:
		bp.MoveUp()
		return true, false
	case tea.KeyDown:
		bp.MoveDown()
		return true, false
	case tea.KeyBackspace:
		if len(bp.filter) > 0 {
			runes := []rune(bp.filter)
			bp.filter = string(runes[:len(runes)-1])
			bp.filterVersion++
			return true, true
		}
		return true, false
	case tea.KeyRunes:
		bp.filter += string(msg.Runes)
		bp.filterVersion++
		return true, true
	case tea.KeySpace:
		bp.filter += " "
		bp.filterVersion++
		return true, true
	}
	return false, false
}

// MoveDown moves the cursor to the next option, reporting false when it is
// already on the last.
func (bp *BranchPicker) MoveDown() bool {
	if bp.cursor >= len(bp.visibleItems())-1 {
		return false
	}
	bp.cursor++
	return true
}

// MoveUp moves the cursor to the previous option, reporting false when it is
// already on the first.
func (bp *BranchPicker) MoveUp() bool {
	if bp.cursor <= 0 {
		return false
	}
	bp.cursor--
	return true
}

// SetResults updates the branch list with search results.
// version must match filterVersion for the results to be accepted (prevents stale updates).
func (bp *BranchPicker) SetResults(branches []string, version uint64) {
	if version != bp.filterVersion {
		return // stale results
	}
	bp.results = branches

	// Hide "New branch" when filter exactly matches a branch name
	bp.showNewBranch = true
	if bp.filter != "" {
		lower := strings.ToLower(bp.filter)
		for _, b := range branches {
			if strings.ToLower(b) == lower {
				bp.showNewBranch = false
				break
			}
		}
	}

	// Clamp cursor
	items := bp.visibleItems()
	if bp.cursor >= len(items) {
		if len(items) > 0 {
			bp.cursor = len(items) - 1
		} else {
			bp.cursor = 0
		}
	}
}

// visibleItems returns the list of items to display.
func (bp *BranchPicker) visibleItems() []string {
	var items []string
	if bp.showNewBranch {
		items = append(items, newBranchOption, noWorktreeOption)
	}
	items = append(items, bp.results...)
	return items
}

// GetSelectedBranch returns the selected branch name, or empty string for either
// of the two pseudo-options, neither of which names a branch.
func (bp *BranchPicker) GetSelectedBranch() string {
	items := bp.visibleItems()
	if bp.cursor < 0 || bp.cursor >= len(items) {
		return ""
	}
	selected := items[bp.cursor]
	if selected == newBranchOption || selected == noWorktreeOption {
		return ""
	}
	return selected
}

// IsNoWorktreeSelected reports whether the session should run in the repository
// itself. Distinct from GetSelectedBranch returning "": that is also what "New
// branch (from HEAD)" reports, and the two do opposite things.
func (bp *BranchPicker) IsNoWorktreeSelected() bool {
	items := bp.visibleItems()
	if bp.cursor < 0 || bp.cursor >= len(items) {
		return false
	}
	return items[bp.cursor] == noWorktreeOption
}

var (
	bpLabelStyle = lipgloss.NewStyle().
			Foreground(theme.Color(theme.Accent)).
			Bold(true)

	bpFilterStyle = lipgloss.NewStyle().
			Foreground(theme.Color(theme.Muted))

	bpSelectedStyle = lipgloss.NewStyle().
			Background(theme.Color(theme.Accent)).
			Foreground(theme.Color(theme.AccentText))

	bpDimStyle = lipgloss.NewStyle().
			Foreground(theme.Color(theme.Subtle))
)

// Render renders the branch picker.
func (bp *BranchPicker) Render() string {
	var s strings.Builder
	s.WriteString(bpLabelStyle.Render("Branch"))
	// Unfocused, the section was a bare label over a list that looks like static
	// text -- nothing said it could be operated, or how to reach it. Focused, the
	// filter cursor appears but typing-to-filter and enter-to-confirm are still
	// only discoverable by trying them.
	if bp.focused {
		cursor := bp.filter + "█"
		s.WriteString(bpFilterStyle.Render(" (filter: " + cursor + ")"))
		s.WriteString(bpDimStyle.Render("   type to filter · ↑↓/tab select · enter choose · ctrl+s start"))
	} else {
		if bp.filter != "" {
			s.WriteString(bpDimStyle.Render(" (filter: " + bp.filter + ")"))
		}
		s.WriteString(bpDimStyle.Render("   tab to choose an existing branch"))
	}
	s.WriteString("\n\n")

	items := bp.visibleItems()
	if len(items) == 0 {
		s.WriteString(bpDimStyle.Render("  No matching branches"))
		return s.String()
	}

	// Show max 5 visible items, windowed around cursor
	maxVisible := 5
	start := 0
	if bp.cursor >= maxVisible {
		start = bp.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(items) {
		end = len(items)
	}

	for i := start; i < end; i++ {
		prefix := "  "
		label := items[i]
		if i == bp.cursor && bp.focused {
			prefix = "> "
			s.WriteString(bpSelectedStyle.Render(prefix + label))
		} else if i == bp.cursor {
			// Still the choice once focus has moved on -- enter here moves it to
			// the button -- so it keeps a mark rather than just a brighter shade.
			s.WriteString("✓ " + label)
		} else {
			s.WriteString(bpDimStyle.Render(prefix + label))
		}
		if i < end-1 {
			s.WriteString("\n")
		}
	}

	return s.String()
}
