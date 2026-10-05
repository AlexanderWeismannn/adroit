package overlay

import (
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	tiStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2)
)

var (
	tiTitleStyle         lipgloss.Style
	tiButtonStyle        lipgloss.Style
	tiFocusedButtonStyle lipgloss.Style
	tiDividerStyle       lipgloss.Style
	tiHintStyle          lipgloss.Style
	tiErrorStyle         lipgloss.Style
)

func init() { theme.OnChange(applyTextInputTheme) }

func applyTextInputTheme() {
	tiStyle = tiStyle.BorderForeground(theme.Color(theme.Accent))
	tiTitleStyle = lipgloss.NewStyle().
		Foreground(theme.Color(theme.Accent)).
		Bold(true).
		MarginBottom(1)
	tiButtonStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	tiFocusedButtonStyle = lipgloss.NewStyle().
		Background(theme.Color(theme.Accent)).
		Foreground(theme.Color(theme.AccentText))
	tiDividerStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	tiHintStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	tiErrorStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
}

// SetError shows a message inside the overlay until the next keypress.
func (t *TextInputOverlay) SetError(msg string) { t.errMsg = msg }

// TextInputOverlay represents a text input overlay with state management.
type TextInputOverlay struct {
	textarea   textarea.Model
	Title      string
	FocusIndex int // index into focusable stops
	Submitted  bool
	Canceled   bool
	OnSubmit   func()
	width      int
	height     int // rows the prompt box would like
	// maxHeight is every row the overlay may take on screen, 0 for no limit. The
	// prompt box gets what the other fields leave of it, up to height.
	maxHeight     int
	profilePicker *ProfilePicker
	branchPicker  *BranchPicker
	numStops      int // total number of focus stops
	// errMsg is shown inside the overlay. An error raised while this is open
	// cannot go to the error box behind it: the overlay covers most of the screen
	// and fades what is left, so a message down there reads as nothing having
	// happened at all.
	errMsg string
}

// NewTextInputOverlay creates a new text input overlay with the given title and initial value.
func NewTextInputOverlay(title string, initialValue string) *TextInputOverlay {
	ti := newTextarea(initialValue)
	return &TextInputOverlay{
		textarea: ti,
		Title:    title,
		numStops: 2, // textarea + enter button
	}
}

// NewTextInputOverlayWithBranchPicker creates a text input overlay that includes an
// empty branch picker. Results are populated asynchronously via SetBranchResults.
func NewTextInputOverlayWithBranchPicker(title string, initialValue string, profiles []config.Profile) *TextInputOverlay {
	ti := newTextarea(initialValue)
	bp := NewBranchPicker()

	var pp *ProfilePicker
	if len(profiles) > 0 {
		pp = NewProfilePicker(profiles)
	}

	numStops := 3 // textarea + branch picker + enter button
	if pp != nil && pp.HasMultiple() {
		numStops = 4 // profile picker + textarea + branch picker + enter button
	}

	overlay := &TextInputOverlay{
		textarea:      ti,
		Title:         title,
		profilePicker: pp,
		branchPicker:  bp,
		numStops:      numStops,
	}
	overlay.updateFocusState()
	return overlay
}

func newTextarea(initialValue string) textarea.Model {
	ti := textarea.New()
	ti.SetValue(initialValue)
	ti.Focus()
	ti.ShowLineNumbers = false
	ti.Prompt = ""
	ti.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ti.CharLimit = 0
	ti.MaxHeight = 0
	return ti
}

func (t *TextInputOverlay) SetSize(width, height int) {
	t.textarea.SetHeight(max(height, 1))
	t.width = width
	t.height = height
	if t.branchPicker != nil {
		t.branchPicker.SetWidth(width - 6)
	}
	if t.profilePicker != nil {
		t.profilePicker.SetWidth(width - 6)
	}
}

// Init initializes the text input overlay model
func (t *TextInputOverlay) Init() tea.Cmd {
	return textarea.Blink
}

// View renders the model's view
func (t *TextInputOverlay) View() string {
	return t.Render()
}

// isProfilePicker returns true if the current focus is on the profile picker.
func (t *TextInputOverlay) isProfilePicker() bool {
	return t.profilePicker != nil && t.profilePicker.HasMultiple() && t.FocusIndex == 0
}

// isTextarea returns true if the current focus is on the textarea.
func (t *TextInputOverlay) isTextarea() bool {
	if t.profilePicker != nil && t.profilePicker.HasMultiple() {
		return t.FocusIndex == 1
	}
	return t.FocusIndex == 0
}

// isEnterButton returns true if the current focus is on the enter button.
func (t *TextInputOverlay) isEnterButton() bool {
	return t.FocusIndex == t.numStops-1
}

// isBranchPicker returns true if the current focus is on the branch picker.
func (t *TextInputOverlay) isBranchPicker() bool {
	if t.branchPicker == nil {
		return false
	}
	if t.profilePicker != nil && t.profilePicker.HasMultiple() {
		return t.FocusIndex == 2
	}
	return t.FocusIndex == 1
}

// FocusBranchPicker opens the overlay with the branch picker focused rather than
// the textarea, for the caller that reached it in order to choose a branch.
//
// No-op without a branch picker, so the caller does not have to know which
// constructor built the overlay.
func (t *TextInputOverlay) FocusBranchPicker() {
	if t.branchPicker == nil {
		return
	}
	if t.profilePicker != nil && t.profilePicker.HasMultiple() {
		t.setFocusIndex(2)
		return
	}
	t.setFocusIndex(1)
}

// setFocusIndex sets the focus index and syncs focus state.
func (t *TextInputOverlay) setFocusIndex(i int) {
	t.FocusIndex = i
	t.updateFocusState()
}

// updateFocusState syncs the textarea/branchPicker/profilePicker focus/blur state.
func (t *TextInputOverlay) updateFocusState() {
	if t.isTextarea() {
		t.textarea.Focus()
	} else {
		t.textarea.Blur()
	}
	if t.branchPicker != nil {
		if t.isBranchPicker() {
			t.branchPicker.Focus()
		} else {
			t.branchPicker.Blur()
		}
	}
	if t.profilePicker != nil {
		if t.isProfilePicker() {
			t.profilePicker.Focus()
		} else {
			t.profilePicker.Blur()
		}
	}
}

// HandleKeyPress processes a key press and updates the state accordingly.
// Returns (shouldClose, branchFilterChanged).
func (t *TextInputOverlay) HandleKeyPress(msg tea.KeyMsg) (bool, bool) {
	// Any key is an attempt to fix whatever was wrong, so the message clears as
	// soon as one arrives rather than sitting there through the correction.
	t.errMsg = ""

	// Submit from whichever field has focus. Enter cannot do it: in the textarea
	// it is a newline and in the branch picker it chooses, so sending took tab,
	// tab, enter -- while the menu said "enter confirm". alt+enter too, where the
	// terminal delivers it.
	if msg.Type == tea.KeyCtrlS || (msg.Type == tea.KeyEnter && msg.Alt) {
		t.Submitted = true
		if t.OnSubmit != nil {
			t.OnSubmit()
		}
		return true, false
	}

	switch msg.Type {
	case tea.KeyTab:
		// In the picker, tab steps down the options and leaves only from the
		// last. Tab is how the picker is reached from the name prompt, so it is
		// the key pressed next to get to "No branch" -- and moving focus off the
		// list instead made that option unreachable with it.
		if t.isBranchPicker() && t.branchPicker.MoveDown() {
			return false, false
		}
		t.setFocusIndex((t.FocusIndex + 1) % t.numStops)
		return false, false
	case tea.KeyShiftTab:
		if t.isBranchPicker() && t.branchPicker.MoveUp() {
			return false, false
		}
		t.setFocusIndex((t.FocusIndex - 1 + t.numStops) % t.numStops)
		return false, false
	case tea.KeyEsc:
		t.Canceled = true
		return true, false
	case tea.KeyEnter:
		if t.isEnterButton() {
			t.Submitted = true
			if t.OnSubmit != nil {
				t.OnSubmit()
			}
			return true, false
		}
		if t.isBranchPicker() {
			// Enter on branch picker = advance to enter button
			t.setFocusIndex(t.numStops - 1)
			return false, false
		}
		if t.isProfilePicker() {
			// Enter on profile picker = advance to textarea
			t.setFocusIndex(t.FocusIndex + 1)
			return false, false
		}
		// Send enter to textarea
		if t.isTextarea() {
			t.textarea, _ = t.textarea.Update(msg)
		}
		return false, false
	default:
		if t.isTextarea() {
			t.textarea, _ = t.textarea.Update(msg)
			return false, false
		}
		if t.isProfilePicker() {
			if msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight {
				t.profilePicker.HandleKeyPress(msg)
			}
			return false, false
		}
		if t.isBranchPicker() {
			_, filterChanged := t.branchPicker.HandleKeyPress(msg)
			return false, filterChanged
		}
		return false, false
	}
}

// GetValue returns the current value of the text input.
func (t *TextInputOverlay) GetValue() string {
	return t.textarea.Value()
}

// GetSelectedBranch returns the selected branch name from the branch picker.
// Returns empty string if no branch picker is present or "New branch" is selected.
func (t *TextInputOverlay) GetSelectedBranch() string {
	if t.branchPicker == nil {
		return ""
	}
	return t.branchPicker.GetSelectedBranch()
}

// GetSelectedProgram returns the program string from the selected profile.
// Returns empty string if no profile picker is present.
func (t *TextInputOverlay) GetSelectedProgram() string {
	if t.profilePicker == nil {
		return ""
	}
	return t.profilePicker.GetSelectedProfile().Program
}

// IsNoWorktreeSelected reports whether the branch picker's "run in the repo
// itself" option is selected. False without a branch picker.
func (t *TextInputOverlay) IsNoWorktreeSelected() bool {
	if t.branchPicker == nil {
		return false
	}
	return t.branchPicker.IsNoWorktreeSelected()
}

// BranchFilterVersion returns the current filter version from the branch picker.
// Returns 0 if no branch picker is present.
func (t *TextInputOverlay) BranchFilterVersion() uint64 {
	if t.branchPicker == nil {
		return 0
	}
	return t.branchPicker.GetFilterVersion()
}

// BranchFilter returns the current filter text from the branch picker.
func (t *TextInputOverlay) BranchFilter() string {
	if t.branchPicker == nil {
		return ""
	}
	return t.branchPicker.GetFilter()
}

// SetBranchResults updates the branch picker with search results.
// version must match the picker's current filterVersion to be accepted.
func (t *TextInputOverlay) SetBranchResults(branches []string, version uint64) {
	if t.branchPicker == nil {
		return
	}
	t.branchPicker.SetResults(branches, version)
}

// IsSubmitted returns whether the form was submitted.
func (t *TextInputOverlay) IsSubmitted() bool {
	return t.Submitted
}

// IsCanceled returns whether the form was canceled.
func (t *TextInputOverlay) IsCanceled() bool {
	return t.Canceled
}

// SetOnSubmit sets a callback function for form submission.
func (t *TextInputOverlay) SetOnSubmit(onSubmit func()) {
	t.OnSubmit = onSubmit
}

// SetMaxHeight bounds the whole overlay to the rows the screen has for it.
//
// The prompt box was sized to 40% of the screen with the profile, branch picker,
// dividers and button stacked on top, so the overlay outgrew any terminal under
// about 50 rows and its bottom -- the branch list, with "No branch" in it, and
// the Enter button -- was drawn off the screen.
func (t *TextInputOverlay) SetMaxHeight(h int) {
	t.maxHeight = h
}

// Render renders the text input overlay.
func (t *TextInputOverlay) Render() string {
	if t.maxHeight <= 0 {
		return t.render()
	}
	// Measure everything but the prompt box with the box at one row, then give
	// the box what is left, never more than it asked for nor less than a row.
	want := max(t.height, 1)
	t.textarea.SetHeight(1)
	fixed := lipgloss.Height(t.render()) - 1
	t.textarea.SetHeight(max(min(want, t.maxHeight-fixed), 1))
	return t.render()
}

func (t *TextInputOverlay) render() string {
	// Inner content width (accounting for padding and borders)
	innerWidth := t.width - 6
	if innerWidth < 1 {
		innerWidth = 1
	}

	// Set textarea width to fit within the overlay
	t.textarea.SetWidth(innerWidth)

	// Build a horizontal divider line
	divider := tiDividerStyle.Render(strings.Repeat("─", innerWidth))

	// Build the view
	var content string

	// Render profile picker if present, above the prompt
	if t.profilePicker != nil {
		content += t.profilePicker.Render() + "\n\n"
		content += divider + "\n\n"
	}

	// Say how to move between the fields, beside the title. The overlay has up to
	// four of them and nothing else announces that tab is what reaches the rest --
	// the branch picker in particular defaults to "New branch", so a user who never
	// presses tab never learns it is a choice at all.
	//
	// The title's own bottom margin is dropped and the blank line written here
	// instead, or the hint lands below the margin rather than next to the title.
	title := tiTitleStyle.UnsetMarginBottom().Render(t.Title)
	if t.numStops > 2 {
		title = lipgloss.JoinHorizontal(lipgloss.Bottom, title,
			tiHintStyle.Render("   tab: next field · esc: cancel"))
	}
	content += title + "\n\n"
	content += t.textarea.View() + "\n\n"

	// Render branch picker if present, with dividers
	if t.branchPicker != nil {
		content += divider + "\n\n"
		content += t.branchPicker.Render() + "\n\n"
	}

	content += divider + "\n\n"

	if t.errMsg != "" {
		content += tiErrorStyle.Render("✗ "+t.errMsg) + "\n\n"
	}

	// Render enter button with appropriate style
	enterButton := " Enter "
	if t.isEnterButton() {
		enterButton = tiFocusedButtonStyle.Render(enterButton)
	} else {
		enterButton = tiButtonStyle.Render(enterButton)
	}
	content += enterButton

	return tiStyle.Render(content)
}
