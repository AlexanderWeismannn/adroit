package overlay

import (
	"github.com/AlexanderWeismannn/adroit/config"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// The branch picker is reached by opening the overlay already focused on it, so
// the caller that wanted a branch does not have to know the stop order. Its index
// shifts when a profile picker takes the first stop, which is the case this is
// really guarding.
func TestFocusBranchPicker(t *testing.T) {
	t.Run("without a profile picker", func(t *testing.T) {
		o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
		require.True(t, o.isTextarea(), "should open on the textarea")

		o.FocusBranchPicker()
		require.True(t, o.isBranchPicker())
		require.False(t, o.isTextarea())
		require.True(t, o.branchPicker.IsFocused(), "the picker itself must take focus, not just the index")
	})

	t.Run("with a profile picker taking the first stop", func(t *testing.T) {
		o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", []config.Profile{
			{Name: "a", Program: "a"}, {Name: "b", Program: "b"},
		})
		o.FocusBranchPicker()
		require.True(t, o.isBranchPicker())
		require.False(t, o.isEnterButton(), "must not overshoot onto the button")
	})

	// So a caller does not have to know which constructor built the overlay.
	t.Run("no-op without a branch picker", func(t *testing.T) {
		o := NewTextInputOverlay("Enter prompt", "")
		o.FocusBranchPicker()
		require.True(t, o.isTextarea())
	})
}

// The picker defaults to "New branch", so a user who never presses tab never
// learns it is a choice. Both states have to say what to do next.
func TestBranchPickerAnnouncesItself(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.SetSize(84, 3)
	o.SetBranchResults([]string{"master"}, 0)

	require.Contains(t, o.Render(), "tab to choose an existing branch",
		"unfocused, nothing said the section could be operated or how to reach it")

	o.FocusBranchPicker()
	focused := o.Render()
	require.Contains(t, focused, "type to filter")
	require.Contains(t, focused, "ctrl+s start")
	require.NotContains(t, focused, "tab to choose an existing branch",
		"once focused, the hint to reach it is stale")
}

// A four-field form whose only hint was its title.
func TestOverlayHeaderNamesTheNavigationKeys(t *testing.T) {
	withPicker := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	withPicker.SetSize(84, 3)
	header := strings.SplitN(withPicker.Render(), "\n", 4)[2]
	require.Contains(t, header, "tab")
	require.Contains(t, header, "esc")

	// Two stops need no explaining: tab reaches the button and that is all.
	plain := NewTextInputOverlay("Enter prompt", "")
	plain.SetSize(84, 3)
	require.NotContains(t, plain.Render(), "next field")
}

// The picker has two pseudo-options that both report no branch and do opposite
// things, so "no branch name" cannot be what distinguishes them.
func TestNoWorktreeOptionIsDistinctFromNewBranch(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.SetSize(84, 3)
	o.SetBranchResults([]string{"master", "TASK-5636-Vis-6"}, 0)
	o.FocusBranchPicker()

	// Cursor starts on "New branch (from HEAD)".
	require.Equal(t, "", o.GetSelectedBranch())
	require.False(t, o.IsNoWorktreeSelected(), "new-branch must not read as no-worktree")

	down := tea.KeyMsg{Type: tea.KeyDown}
	o.HandleKeyPress(down)
	require.Equal(t, "", o.GetSelectedBranch(), "the no-worktree option names no branch")
	require.True(t, o.IsNoWorktreeSelected())

	// A real branch is neither.
	o.HandleKeyPress(down)
	require.Equal(t, "master", o.GetSelectedBranch())
	require.False(t, o.IsNoWorktreeSelected())
}

// It is offered where the choice is made, so it has to be visible there.
func TestNoWorktreeOptionIsListed(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.SetSize(84, 3)
	o.SetBranchResults([]string{"master"}, 0)
	require.Contains(t, o.Render(), noWorktreeOption)
}

// An error raised while the overlay is open has to be shown inside it. The error
// box is behind the overlay and faded by it, so a message there is invisible in
// practice — which is exactly how a refused submit came to look like a dead key.
func TestOverlayShowsItsOwnErrors(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.SetSize(84, 3)
	o.SetBranchResults([]string{"master"}, 0)

	require.NotContains(t, o.Render(), "✗")

	o.SetError(`a session for branch "master" already exists`)
	require.Contains(t, o.Render(), `✗ a session for branch "master" already exists`)

	// Any key is an attempt to fix it, so the message goes rather than sitting
	// there through the correction.
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
	require.NotContains(t, o.Render(), "✗")
}

// ctrl+s sends from any field: enter is a newline in the textarea and a choice
// in the branch picker, so it could not.
func TestCtrlSSubmitsFromAnyField(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.SetSize(84, 10)
	closed, _ := o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyCtrlS})
	require.True(t, closed)
	require.True(t, o.IsSubmitted())

	o = NewTextInputOverlayWithBranchPicker("Enter prompt", "", nil)
	o.FocusBranchPicker()
	closed, _ = o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyCtrlS})
	require.True(t, closed && o.IsSubmitted())
}

// Enter on the picker moves focus on to the button, and the picker's cursor row
// used to lose its marker with the focus -- rendered only a shade brighter than
// the rows around it. Choosing "No branch" and pressing enter then showed
// nothing chosen at all, and read as the choice not having taken.
func TestTheChosenBranchStaysMarkedAfterFocusMovesOn(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt (optional)", "", nil)
	o.FocusBranchPicker()
	o.SetSize(80, 5)
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})

	require.True(t, o.IsNoWorktreeSelected())
	require.Contains(t, o.Render(), "✓ No branch (run in the repo itself)")
}

// Tab from the name prompt lands on the picker, and the natural next press to
// get to "No branch" is tab again -- which moved focus straight off the picker
// to the Enter button, so the option below the cursor could not be reached that
// way at all. Inside the picker tab and shift+tab now step through the options,
// and only leave the field from the ends of the list.
func TestTabStepsThroughTheBranchOptions(t *testing.T) {
	o := NewTextInputOverlayWithBranchPicker("Enter prompt (optional)", "", nil)
	o.FocusBranchPicker()
	o.SetBranchResults([]string{"feature"}, o.BranchFilterVersion())

	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyTab})
	require.True(t, o.isBranchPicker(), "tab inside the list must stay in the list")
	require.True(t, o.IsNoWorktreeSelected(), "one tab from the top is \"No branch\"")

	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyTab})
	require.Equal(t, "feature", o.GetSelectedBranch())
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyTab})
	require.True(t, o.isEnterButton(), "tab from the last option moves on to the next field")

	o.FocusBranchPicker()
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyShiftTab})
	require.True(t, o.IsNoWorktreeSelected(), "shift+tab steps back up the list")
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyShiftTab})
	o.HandleKeyPress(tea.KeyMsg{Type: tea.KeyShiftTab})
	require.True(t, o.isTextarea(), "shift+tab from the top option moves back to the prompt")
}
