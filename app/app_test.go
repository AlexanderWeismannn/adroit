package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/resume"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	// Run all tests
	exitCode := m.Run()

	// Exit with the same code as the tests
	os.Exit(exitCode)
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("shows confirmation on D press", func(t *testing.T) {
		// Simulate pressing 'D'
		h.state = stateDefault
		h.confirmationOverlay = nil

		// Manually trigger what would happen in handleKeyPress for 'D'
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill session 'test'?")

		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
	})

	t.Run("returns to default on y press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'y' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on n press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'n' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on esc press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing ESC using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyEscape}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})
}

// TestConfirmationModalKeyHandling tests the actual key handling in confirmation state
func TestConfirmationModalKeyHandling(t *testing.T) {
	// Import needed packages
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := &home{
		ctx:                 context.Background(),
		state:               stateConfirm,
		appConfig:           config.DefaultConfig(),
		list:                list,
		menu:                ui.NewMenu(),
		confirmationOverlay: overlay.NewConfirmationOverlay("Kill session?"),
	}

	testCases := []struct {
		name              string
		key               string
		expectedState     state
		expectedDismissed bool
		expectedNil       bool
	}{
		{
			name:              "y key confirms and dismisses overlay",
			key:               "y",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "n key cancels and dismisses overlay",
			key:               "n",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "esc key cancels and dismisses overlay",
			key:               "esc",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "other keys are ignored",
			key:               "x",
			expectedState:     stateConfirm,
			expectedDismissed: false,
			expectedNil:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset state
			h.state = stateConfirm
			h.confirmationOverlay = overlay.NewConfirmationOverlay("Kill session?")

			// Create key message
			var keyMsg tea.KeyMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyMsg{Type: tea.KeyEscape}
			} else {
				keyMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)}
			}

			// Call handleKeyPress
			model, _ := h.handleKeyPress(keyMsg)
			homeModel, ok := model.(*home)
			require.True(t, ok)

			assert.Equal(t, tc.expectedState, homeModel.state, "State mismatch for key: %s", tc.key)
			if tc.expectedNil {
				assert.Nil(t, homeModel.confirmationOverlay, "Overlay should be nil for key: %s", tc.key)
			} else {
				assert.NotNil(t, homeModel.confirmationOverlay, "Overlay should not be nil for key: %s", tc.key)
				assert.Equal(t, tc.expectedDismissed, homeModel.confirmationOverlay.Dismissed, "Dismissed mismatch for key: %s", tc.key)
			}
		})
	}
}

// TestConfirmationMessageFormatting tests that confirmation messages are formatted correctly
func TestConfirmationMessageFormatting(t *testing.T) {
	testCases := []struct {
		name            string
		sessionTitle    string
		expectedMessage string
	}{
		{
			name:            "short session name",
			sessionTitle:    "my-feature",
			expectedMessage: "[!] Kill session 'my-feature'? (y/n)",
		},
		{
			name:            "long session name",
			sessionTitle:    "very-long-feature-branch-name-here",
			expectedMessage: "[!] Kill session 'very-long-feature-branch-name-here'? (y/n)",
		},
		{
			name:            "session with spaces",
			sessionTitle:    "feature with spaces",
			expectedMessage: "[!] Kill session 'feature with spaces'? (y/n)",
		},
		{
			name:            "session with special chars",
			sessionTitle:    "feature/branch-123",
			expectedMessage: "[!] Kill session 'feature/branch-123'? (y/n)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the message formatting directly
			actualMessage := fmt.Sprintf("[!] Kill session '%s'? (y/n)", tc.sessionTitle)
			assert.Equal(t, tc.expectedMessage, actualMessage)
		})
	}
}

// TestConfirmationFlowSimulation tests the confirmation flow by simulating the state changes
func TestConfirmationFlowSimulation(t *testing.T) {
	// Create a minimal setup
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
		AutoYes: false,
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
	}

	// Simulate what happens when D is pressed
	selected := h.list.GetSelectedInstance()
	require.NotNil(t, selected)

	// This is what the KeyKill handler does
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the state
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	// Test that overlay renders with the correct message
	rendered := h.confirmationOverlay.Render()
	assert.Contains(t, rendered, "Kill session 'test-session'?")
}

// TestConfirmActionWithDifferentTypes tests that confirmAction works with different action types
func TestConfirmActionWithDifferentTypes(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("works with simple action returning nil", func(t *testing.T) {
		actionCalled := false
		action := func() tea.Msg {
			actionCalled = true
			return nil
		}

		// Set up callback to track action execution
		actionExecuted := false
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			actionExecuted = true
			action() // Execute the action
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.True(t, actionCalled)
		assert.True(t, actionExecuted)
	})

	t.Run("works with action returning error", func(t *testing.T) {
		expectedErr := fmt.Errorf("test error")
		action := func() tea.Msg {
			return expectedErr
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Error action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.Equal(t, expectedErr, receivedMsg)
	})

	t.Run("works with action returning custom message", func(t *testing.T) {
		action := func() tea.Msg {
			return instanceChangedMsg{}
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Custom message action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		_, ok := receivedMsg.(instanceChangedMsg)
		assert.True(t, ok, "Expected instanceChangedMsg but got %T", receivedMsg)
	})
}

// TestMultipleConfirmationsDontInterfere tests that multiple confirmations don't interfere with each other
func TestMultipleConfirmationsDontInterfere(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// First confirmation
	action1Called := false
	action1 := func() tea.Msg {
		action1Called = true
		return nil
	}

	// Set up first confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("First action?")
	firstOnConfirm := func() {
		h.state = stateDefault
		action1()
	}
	h.confirmationOverlay.OnConfirm = firstOnConfirm
	h.state = stateConfirm

	// Verify first confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Cancel first confirmation (simulate pressing 'n')
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
	if shouldClose {
		h.state = stateDefault
		h.confirmationOverlay = nil
	}

	// Second confirmation with different action
	action2Called := false
	action2 := func() tea.Msg {
		action2Called = true
		return fmt.Errorf("action2 error")
	}

	// Set up second confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("Second action?")
	var secondResult tea.Msg
	secondOnConfirm := func() {
		h.state = stateDefault
		secondResult = action2()
	}
	h.confirmationOverlay.OnConfirm = secondOnConfirm
	h.state = stateConfirm

	// Verify second confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Execute second action to verify it's the correct one
	h.confirmationOverlay.OnConfirm()
	err, ok := secondResult.(error)
	assert.True(t, ok)
	assert.Equal(t, "action2 error", err.Error())
	assert.True(t, action2Called)
	assert.False(t, action1Called, "First action should not have been called")

	// Test that cancelled action can still be executed independently
	firstOnConfirm()
	assert.True(t, action1Called, "First action should be callable after being replaced")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// Create a test confirmation overlay
	message := "[!] Delete everything?"
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the overlay was created with confirmation settings
	assert.NotNil(t, h.confirmationOverlay)
	assert.Equal(t, stateConfirm, h.state)
	assert.False(t, h.confirmationOverlay.Dismissed)

	// Test the overlay render (we can test that it renders without errors)
	rendered := h.confirmationOverlay.Render()
	assert.NotEmpty(t, rendered)

	// Test that it includes the message content and instructions
	assert.Contains(t, rendered, "Delete everything?")
	assert.Contains(t, rendered, "Press")
	assert.Contains(t, rendered, "to confirm")
	assert.Contains(t, rendered, "to cancel")

	// Test that the danger indicator is preserved
	assert.Contains(t, rendered, "[!")
}

// Tabbing straight to the branch picker leaves the session unnamed, and the
// branch it opens is the name anyone would have typed — so the name is derived
// rather than demanded.
func TestTitleForBranch(t *testing.T) {
	existing := func(titles ...string) []*session.Instance {
		out := make([]*session.Instance, 0, len(titles))
		for _, title := range titles {
			inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
			require.NoError(t, err)
			out = append(out, inst)
		}
		return out
	}

	t.Run("takes the branch name verbatim", func(t *testing.T) {
		// Not truncated to the name prompt's 32 characters: that is a typing
		// convenience, and the list truncates a long title for display already.
		long := "TASK-5602_delegation-and-admin-invite-authz"
		title, err := titleForBranch(long, nil)
		require.NoError(t, err)
		require.Equal(t, long, title)
	})

	t.Run("refuses when no branch was chosen", func(t *testing.T) {
		// What "New branch (from HEAD)" reports. With no name typed either there
		// is nothing to derive from, so the submit has to be refused.
		_, err := titleForBranch("", existing())
		require.Error(t, err)
		require.Contains(t, err.Error(), "type a name")
	})

	t.Run("refuses a branch that already has a session", func(t *testing.T) {
		// Caught here rather than surfacing later as "tmux session already
		// exists": Title is both the storage key and the tmux session name.
		_, err := titleForBranch("TASK-5636-Vis-6", existing("other", "TASK-5636-Vis-6"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists")
	})

	t.Run("allows a branch whose name no session is using", func(t *testing.T) {
		title, err := titleForBranch("TASK-5636-Vis-6", existing("other"))
		require.NoError(t, err)
		require.Equal(t, "TASK-5636-Vis-6", title)
	})
}

// Titles that tmux turns into the same session name are one session: both rows
// attach to the same agent and killing either kills both. The typed name has to
// be refused before anything is created for it.
func TestTitleCollisionComparesTmuxNames(t *testing.T) {
	mk := func(title string) *session.Instance {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		return inst
	}
	existing := []*session.Instance{mk("fixlogin"), mk("api.v2"), mk("docs")}

	for _, title := range []string{"fix login", "api:v2", "api v2"} {
		inst := mk(title)
		if title == "api v2" {
			// Only whitespace is dropped; "apiv2" is a different tmux name from "api_v2".
			require.NoError(t, titleCollision(inst, existing), title)
			continue
		}
		err := titleCollision(inst, existing)
		require.Error(t, err, title)
		require.Contains(t, err.Error(), "too close", title)
	}

	err := titleCollision(mk("docs"), existing)
	require.ErrorContains(t, err, "already exists")

	// Nor may a name land on another session's Terminal-tab session.
	require.ErrorContains(t, titleCollision(mk("term_docs"), existing), "Terminal tab")
	require.ErrorContains(t, titleCollision(mk("x"), append(existing, mk("term_x"))), "Terminal tab")

	// The row being named is in the list too, and must not collide with itself.
	self := mk("docs-2")
	require.NoError(t, titleCollision(self, append(existing, self)))
}

// Cancelling has to put back the palette that was in use, and it is the app that
// installs previews, so it is the app that must restore. A picker that both
// previewed and remembered would have to get this right in every exit path.
func TestThemePickerPreviewAndRestore(t *testing.T) {
	original, _ := theme.Builtin("default")
	theme.Set(original)
	t.Cleanup(func() { theme.Set(original) })

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
	}

	t.Run("cancel restores what was in use", func(t *testing.T) {
		theme.Set(original)
		h.themeBeforePicker = theme.Current()
		h.themePicker = overlay.NewThemePicker("default")

		h.previewTheme("dracula")
		dracula, _ := theme.Builtin("dracula")
		require.Equal(t, dracula[theme.Accent], theme.Current()[theme.Accent],
			"the preview did not reach the palette")

		h.closeThemePicker(true)
		require.Equal(t, original[theme.Accent], theme.Current()[theme.Accent])
		require.Nil(t, h.themePicker)
		require.Equal(t, stateDefault, h.state)
	})

	t.Run("keeping leaves the previewed palette installed", func(t *testing.T) {
		theme.Set(original)
		h.themeBeforePicker = theme.Current()
		h.themePicker = overlay.NewThemePicker("default")

		h.previewTheme("gruvbox")
		h.closeThemePicker(false)

		gruvbox, _ := theme.Builtin("gruvbox")
		require.Equal(t, gruvbox[theme.Accent], theme.Current()[theme.Accent])
	})

	t.Run("a preview keeps the user's own overrides", func(t *testing.T) {
		// Otherwise the preview shows the theme's value for a role the user has
		// replaced, and confirming would change the interface again afterwards.
		theme.Set(original)
		h.appConfig.Colors = map[string]theme.Pair{
			"accent": {Light: "#ff0000", Dark: "#ff0000"},
		}
		t.Cleanup(func() { h.appConfig.Colors = nil })

		h.previewTheme("dracula")
		require.Equal(t, theme.Pair{Light: "#ff0000", Dark: "#ff0000"}, theme.Current()[theme.Accent])

		dracula, _ := theme.Builtin("dracula")
		require.Equal(t, dracula[theme.Success], theme.Current()[theme.Success],
			"roles the user has not overridden should still come from the theme")
	})
}

// Pressing tab then enter on "No branch" used to refuse the submit and report
// "type a name" through the error box — which sits behind the overlay, faded, so
// it read as the key having done nothing at all. There is always a directory to
// name the session after, so there is nothing to refuse.
func TestTitleForRepo(t *testing.T) {
	existing := func(titles ...string) []*session.Instance {
		out := make([]*session.Instance, 0, len(titles))
		for _, title := range titles {
			inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
			require.NoError(t, err)
			out = append(out, inst)
		}
		return out
	}

	require.Equal(t, "myapp", titleForRepo("/home/jane/myapp", nil))

	// Several scratch terminals on one checkout is the point, so a repeat takes a
	// suffix rather than being refused the way a duplicate branch is.
	require.Equal(t, "myapp-2", titleForRepo("/home/jane/myapp", existing("myapp")))
	require.Equal(t, "myapp-3", titleForRepo("/home/jane/myapp", existing("myapp", "myapp-2")))

	// It skips a taken suffix rather than stopping at the first gap-free number.
	require.Equal(t, "myapp-2", titleForRepo("/home/jane/myapp", existing("myapp", "myapp-3")))

	// A path with no usable base still yields something startable, since Title
	// is the tmux session name and an empty one cannot be created.
	require.Equal(t, "repo", titleForRepo("/", nil))
	require.NotEmpty(t, titleForRepo(".", nil))
}

// fakeInstanceStorage is an in-memory config.InstanceStorage, so a test can
// exercise storage.DeleteInstance without touching the user's real state file.
type fakeInstanceStorage struct {
	instances json.RawMessage
}

func (f *fakeInstanceStorage) SaveInstances(instancesJSON json.RawMessage) error {
	f.instances = instancesJSON
	return nil
}

func (f *fakeInstanceStorage) GetInstances() json.RawMessage {
	if f.instances == nil {
		return json.RawMessage("[]")
	}
	return f.instances
}

func (f *fakeInstanceStorage) DeleteAllInstances() error {
	f.instances = json.RawMessage("[]")
	return nil
}

// TestConfirmedActionMessageReachesUpdateLoop pins the delivery of a confirmed
// action's message. The overlay's OnConfirm callback returns nothing, so the
// action has to be parked on the model and handed back as a command on the way
// out of stateConfirm. Drop that and every confirmed action fails silently: a
// kill refused because its branch is checked out, or a push that could not
// commit, is indistinguishable from one that worked.
//
// It is the ACTION that is parked, not the message it produced. Running it in
// OnConfirm runs it on the update loop, and a kill's teardown -- tmux, git,
// the dev stack -- then holds every keystroke and every frame until it is done.
func TestConfirmedActionMessageReachesUpdateLoop(t *testing.T) {
	newHomeForConfirm := func() *home {
		spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
		return &home{
			ctx:       context.Background(),
			state:     stateDefault,
			appConfig: config.DefaultConfig(),
			menu:      ui.NewMenu(),
			errBox:    ui.NewErrBox(),
			list:      ui.NewList(&spin, false),
		}
	}

	t.Run("confirming delivers the action's message", func(t *testing.T) {
		h := newHomeForConfirm()
		actionErr := fmt.Errorf("instance test-session is currently checked out")

		h.confirmAction("[!] Kill session 'test-session'?", func() tea.Msg { return actionErr })
		require.Equal(t, stateConfirm, h.state)

		_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		require.NotNil(t, cmd, "a confirmed action's message must reach the update loop")
		assert.Equal(t, actionErr, cmd())

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.pendingAction, "the action must not be delivered twice")
	})

	// The guard on the freeze: confirming must hand the action back, not run it.
	// Bubble Tea calls the returned Cmd on a goroutine of its own, so anything
	// slow inside it costs the user nothing; the same work done in OnConfirm is
	// done on the update loop, where it blocks input and rendering alike.
	t.Run("confirming does not run the action on the loop", func(t *testing.T) {
		h := newHomeForConfirm()
		ran := make(chan struct{})

		h.confirmAction("[!] Kill session 'test-session'?", func() tea.Msg {
			close(ran)
			return nil
		})

		_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		select {
		case <-ran:
			t.Fatal("the action ran on the update loop; it must be returned as a Cmd")
		default:
		}

		require.NotNil(t, cmd)
		cmd()
		select {
		case <-ran:
		default:
			t.Fatal("the returned Cmd did not run the action")
		}
	})

	t.Run("cancelling runs nothing and delivers nothing", func(t *testing.T) {
		h := newHomeForConfirm()
		called := false

		h.confirmAction("[!] Kill session 'test-session'?", func() tea.Msg {
			called = true
			return fmt.Errorf("should never run")
		})

		_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		assert.False(t, called)
		assert.Nil(t, cmd)
		assert.Nil(t, h.pendingAction)
	})
}

// TestKillNoWorktreeSession pins that a session running in the repository itself
// can be killed. It has no worktree, so the branch-checked-out check cannot run
// against one: asking for the worktree returns an error, and returning that
// error aborts the kill before the list and storage are touched -- leaving the
// session on screen, and still in state.json after a restart.
func TestKillNoWorktreeSession(t *testing.T) {
	// A title of its own, since killing it runs tmux kill-session against it.
	const title = "kill-no-worktree-test"

	storageState := &fakeInstanceStorage{
		instances: json.RawMessage(fmt.Sprintf(
			`[{"title":%q,"path":%q,"branch":"","status":%d,"no_worktree":true,"program":"claude"}]`,
			title, t.TempDir(), session.Paused)),
	}
	storage, err := session.NewStorage(storageState)
	require.NoError(t, err)

	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spin, false)

	// Built from stored data rather than NewInstance, and Paused, so that it is
	// started (without a tmux session having to exist) and carries no worktree.
	// Both matter: an unstarted instance would be refused by GetGitWorktree for
	// the wrong reason, and the test would pass without pinning the guard.
	instance, err := session.FromInstanceData(session.InstanceData{
		Title:      title,
		Path:       t.TempDir(),
		Status:     session.Paused,
		NoWorktree: true,
		Program:    "claude",
	})
	require.NoError(t, err)
	require.True(t, instance.Started())
	require.True(t, instance.NoWorktree())
	_, worktreeErr := instance.GetGitWorktree()
	require.Error(t, worktreeErr, "the kill path must not depend on this succeeding")
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		errBox:    ui.NewErrBox(),
		list:      list,
		storage:   storage,
		devStack:  dev.New(nil),
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(dev.New(nil))),
	}

	// The first press only highlights the menu entry and re-sends the key; the
	// second is the one that reaches the KeyKill handler.
	killKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}}
	_, _ = h.handleKeyPress(killKey)
	_, _ = h.handleKeyPress(killKey)
	require.Equal(t, stateConfirm, h.state)

	_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	require.NotNil(t, cmd)

	// A kill is two steps now. The confirmed action runs off the loop and only
	// reports what it found; the model is changed when its message comes back.
	msg := cmd()
	if err, ok := msg.(error); ok {
		t.Fatalf("killing a worktree-less session returned an error: %v", err)
	}
	approved, ok := msg.(killApprovedMsg)
	require.True(t, ok, "the checks must approve the kill, got %T", msg)
	require.Equal(t, title, approved.instance.Title)

	// Nothing has been torn down yet: approval is only permission.
	require.Equal(t, 1, h.list.NumInstances(), "the session must survive until the loop acts on the approval")

	teardown := h.killApproved(approved.instance)
	require.NotNil(t, teardown)

	assert.Equal(t, 0, h.list.NumInstances(), "the session must be gone from the list")
	assert.NotContains(t, string(storageState.GetInstances()), title,
		"the session must be gone from storage, or it returns on restart")

	// The tmux session and the worktree go on the Cmd, after the list and storage
	// are already right -- that ordering is what keeps a stuck teardown off the
	// interface.
	teardown()
}

// fakeAppState records what the app persists about the dev stack.
type fakeAppState struct {
	helpSeen  uint32
	devStack  string
	devWrites int
	killed    []config.KilledSession
}

func (f *fakeAppState) GetHelpScreensSeen() uint32                { return f.helpSeen }
func (f *fakeAppState) SetHelpScreensSeen(s uint32) error         { f.helpSeen = s; return nil }
func (f *fakeAppState) GetDevStackInstance() string               { return f.devStack }
func (f *fakeAppState) SetDevStackInstance(t string) error        { f.devStack = t; f.devWrites++; return nil }
func (f *fakeAppState) GetKilledSessions() []config.KilledSession { return f.killed }
func (f *fakeAppState) AddKilledSession(k config.KilledSession) error {
	f.killed = append([]config.KilledSession{k}, f.killed...)
	return nil
}

func newDevStackHome(t *testing.T, stack *dev.Stack, state *fakeAppState) *home {
	t.Helper()
	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		errBox:    ui.NewErrBox(),
		list:      ui.NewList(&spin, false),
		appState:  state,
		devStack:  stack,
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(stack)),
	}
}

// Pressing the stop key with nothing running must do nothing at all -- in
// particular it must not clear a record that is not there to clear, which is
// what the next run adopts a still-live stack from.
func TestStopDevStackIsANoOpWhenNothingIsRunning(t *testing.T) {
	state := &fakeAppState{devStack: "left-over"}
	h := newDevStackHome(t, dev.New(nil), state)

	require.Nil(t, h.stopDevStack())
	require.Equal(t, 0, state.devWrites, "no stack was running; nothing should have been written")
	require.Equal(t, "left-over", state.devStack)
}

// A failed start must not leave a record behind: the next run adopts by that
// title and would find a stack that never existed.
func TestAFailedStartClearsTheRecordedSession(t *testing.T) {
	state := &fakeAppState{}
	h := newDevStackHome(t, dev.New(nil), state)
	state.devStack = "TASK-1"

	_, cmd := h.Update(devStackStartedMsg{title: "TASK-1", err: errors.New("boom")})
	require.NotNil(t, cmd)
	require.Equal(t, "", state.devStack, "a failed start must clear the record")
}

// The composed view has to be exactly the height it was given. View pads a blank
// row above each of the list and the preview, and that row was unbudgeted, so
// every frame came out one row taller than the terminal and the whole thing
// scrolled by one on each redraw.
func TestTheViewIsExactlyTheTerminalHeight(t *testing.T) {
	for _, size := range [][2]int{{150, 24}, {150, 30}, {150, 40}, {110, 30}, {90, 26}} {
		h := newSizedHome(t, size[0], size[1])
		lines := strings.Split(h.View(), "\n")
		require.Len(t, lines, size[1], "%dx%d", size[0], size[1])
		for i, line := range lines {
			require.LessOrEqual(t, lipgloss.Width(line), size[0],
				"%dx%d row %d overruns the terminal", size[0], size[1], i)
		}
	}
}

// And exactly the terminal width, at any width. The list column and the window
// each keep to the budget they were given -- but nothing cut either to it, so a
// title or a row wider than its column ("Instances · 4" is 15 cells against the
// 12 the list gets at 40 columns) pushed the pane beside it off the screen, and
// tmux clipped the interface's right-hand edge.
func TestTheViewIsNeverWiderThanTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{20, 20}, {30, 20}, {40, 24}, {50, 24}, {60, 20}, {80, 30}, {150, 40}} {
		h := newSizedHome(t, size[0], size[1])
		for _, state := range []state{stateDefault, stateConfirm, stateHelp} {
			h.state = state
			h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill session 'TASK-5756-a-long-session-name'?")
			h.textOverlay = overlay.NewTextOverlay("Instance Created")
			for i, line := range strings.Split(h.View(), "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), size[0],
					"%dx%d state %d row %d overruns the terminal: %q", size[0], size[1], state, i, line)
			}
		}
	}
}

// While a name is being typed the row owns every printable key. Most letters are
// also shortcuts -- n, r, t, d, x, p, c, g, o -- so without this, typing
// "post-ai-svg" highlighted a different menu entry on nearly every keystroke and
// re-sent each one through the update loop to get it delivered.
func TestNamingKeystrokesAreNotStolenByTheMenu(t *testing.T) {
	h := newSizedHome(t, 150, 40)
	press := func(r rune) { _, _ = h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}) }
	press('n')
	press('n') // the first press only highlights the menu entry and re-sends

	const name = "post-ai-svg" // p, o, t, and n are all shortcuts
	for _, r := range name {
		press(r)
	}

	instances := h.list.GetInstances()
	require.Equal(t, name, instances[len(instances)-1].Title,
		"a keystroke was intercepted instead of reaching the name")
}

func newSizedHome(t *testing.T, w, hgt int) *home {
	t.Helper()
	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	stack := dev.New(nil)
	h := &home{
		ctx: context.Background(), state: stateDefault, appConfig: config.DefaultConfig(),
		menu: ui.NewMenu(), errBox: ui.NewErrBox(), spinner: spin, devStack: stack,
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(stack)),
	}
	h.list = ui.NewList(&h.spinner, false)
	repo := t.TempDir()
	for i := 0; i < 4; i++ {
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: fmt.Sprintf("TASK-57%02d-session", i), Path: repo,
			Status: session.Paused, Program: "claude", NoWorktree: true})
		require.NoError(t, err)
		h.list.AddInstance(inst)()
	}
	h.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: w, Height: hgt})
	return h
}

// fakeTmuxExec records the tmux commands the reaper issues.
type fakeTmuxExec struct {
	sessions []string
	killed   []string
}

func (f *fakeTmuxExec) Run(c *exec.Cmd) error {
	if len(c.Args) >= 4 && c.Args[1] == "kill-session" {
		f.killed = append(f.killed, strings.TrimPrefix(c.Args[3], "="))
	}
	return nil
}
func (f *fakeTmuxExec) Output(c *exec.Cmd) ([]byte, error) {
	return []byte(strings.Join(f.sessions, "\n")), nil
}
func (f *fakeTmuxExec) Start(*exec.Cmd) error { return nil }
func (f *fakeTmuxExec) Wait(*exec.Cmd) error  { return nil }

// A Terminal-tab shell outlives the process that made it, and nothing used to
// look at the ones left behind: they accumulated for as long as the tmux server
// lived. Seven were found on this machine, for sessions killed days earlier.
func TestOrphanedTerminalsAreReapedAtStartup(t *testing.T) {
	live := []string{"post-ai-svg", "TASK-5635-UI"}
	instances := make([]*session.Instance, 0, len(live))
	for _, title := range live {
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: title, Path: t.TempDir(), Status: session.Paused, Program: "claude", NoWorktree: true})
		require.NoError(t, err)
		instances = append(instances, inst)
	}

	fake := &fakeTmuxExec{sessions: []string{
		"adroit_term_post-ai-svg",    // live: keep
		"adroit_term_TASK-5635-UI",   // live: keep
		"adroit_term_TASK-5719-gone", // orphan: reap
		"adroit_term_TASK-5632-gone", // orphan: reap
		"adroit_post-ai-svg",         // an agent's own session: never touched
		"adroit_TASK-5719-gone",      // an agent whose instance is gone: still not ours to kill
		"adroit_dev",                 // the dev stack: not a terminal
		"main",                       // not ours at all
	}}

	reapOrphanedTerminalsWith(fake, instances)

	require.ElementsMatch(t, []string{"adroit_term_TASK-5719-gone", "adroit_term_TASK-5632-gone"}, fake.killed)
}

// An agent session carries a live conversation, and a session missing from the
// list is not proof its work is finished -- pausing and resuming rely on it
// still being there.
func TestReapingNeverTouchesAnAgentSession(t *testing.T) {
	fake := &fakeTmuxExec{sessions: []string{"adroit_TASK-5719-gone", "adroit_dev", "main"}}
	reapOrphanedTerminalsWith(fake, nil)
	require.Empty(t, fake.killed)

	// An agent whose title starts "term_" has the terminals' prefix. It is still
	// an agent, not an orphaned terminal.
	inst, err := session.NewInstance(session.InstanceOptions{Title: "term_sheet", Path: ".", Program: "echo"})
	require.NoError(t, err)
	fake = &fakeTmuxExec{sessions: []string{"adroit_term_sheet"}}
	reapOrphanedTerminalsWith(fake, []*session.Instance{inst})
	require.Empty(t, fake.killed, "the agent of a session titled term_sheet was reaped")
}

// The tick's whole cost is what it decides to spend here, so the plan is worth
// asserting directly: the expensive half of a tick is two git subprocesses per
// session, and every one of them is spent because this said so.
func TestPlanMetadataSpendsNothingOnAFreshDiff(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)

	// Nothing computed yet: the first tick must do the work.
	plans := h.planMetadata([]*session.Instance{inst})
	require.Len(t, plans, 1)
	require.True(t, plans[0].diffDue, "a session with no diff yet has to be computed")
	require.True(t, plans[0].branchDue)

	// Having just computed it, the next tick has nothing to do -- the pane-changed
	// gate at the call site is what will ask for the next one.
	inst.SetDiffStats(&git.DiffStats{Added: 3}, false)
	inst.SetCurrentBranch("TASK-1")
	plans = h.planMetadata([]*session.Instance{inst})
	require.False(t, plans[0].diffDue)
	require.False(t, plans[0].branchDue)
}

// The full diff text is only ever rendered by the diff pane. Loading it for the
// selected session on every tick regardless of which tab was open meant a whole
// diff read into memory twice a second to be thrown away.
func TestPlanMetadataWantsDiffContentOnlyForTheDiffTab(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)
	h.list.SetSelectedInstance(0)
	inst.SetDiffStats(&git.DiffStats{Added: 3}, false)

	require.False(t, h.planMetadata([]*session.Instance{inst})[0].wantContent,
		"the preview tab needs counts, not content")

	h.tabbedWindow.Toggle() // preview -> diff
	require.True(t, h.tabbedWindow.IsInDiffTab())
	plan := h.planMetadata([]*session.Instance{inst})[0]
	require.True(t, plan.wantContent)
	require.True(t, plan.diffDue, "content is wanted and what we have has none, so it is stale")
}

// An unselected row's line count is decoration on a line that is mostly branch
// name; the row being read gets the tighter floor.
func TestPlanMetadataGivesTheSelectedRowTheTighterFloor(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	var insts []*session.Instance
	for _, title := range []string{"a", "b"} {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "echo"})
		require.NoError(t, err)
		inst.SetDiffStats(&git.DiffStats{Added: 1}, false)
		h.list.AddInstance(inst)
		insts = append(insts, inst)
	}
	h.list.SetSelectedInstance(0)

	require.Less(t, diffMaxAgeSelected, diffMaxAgeOther)
	// Both are fresh, so neither is due; the floors themselves are what differ,
	// and DiffStale is where they are applied.
	require.False(t, insts[0].DiffStale(diffMaxAgeSelected, false))
	require.True(t, insts[0].DiffStale(0, false), "any age at all exceeds a zero floor")
}

// The animation used to re-tick forever, so an interface with nothing moving in
// it still re-rendered twelve times a second for the life of the process.
func TestSpinnerNeededOnlyWhileSomethingAnimates(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)

	inst.SetStatus(session.Ready)
	inst.SetActivity(session.ActivityIdle, 0)
	require.False(t, h.spinnerNeeded(), "an idle list animates nothing")

	inst.SetActivity(session.ActivityWorking, 0)
	require.True(t, h.spinnerNeeded())

	inst.SetActivity(session.ActivityIdle, 1)
	require.False(t, h.spinnerNeeded())
	inst.SetActivity(session.ActivityShell, 1)
	require.True(t, h.spinnerNeeded(), "a background shell still spins")

	inst.SetActivity(session.ActivityIdle, 0)
	inst.SetStatus(session.Loading)
	require.True(t, h.spinnerNeeded(), "a session being rebuilt spins")
}

// "The pane changed" is a much weaker signal than it looks: an agent mid-turn
// redraws continuously — its own spinner is enough — so the gate is open on
// every tick for exactly the sessions that are busy. Without a ceiling those
// sessions pay the full twice-a-second cost the gate exists to avoid.
func TestPlanMetadataRateLimitsAChangingPane(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)
	h.list.SetSelectedInstance(0)
	inst.SetDiffStats(&git.DiffStats{Added: 1}, false)

	plan := h.planMetadata([]*session.Instance{inst})[0]
	require.False(t, plan.diffDue, "the floor has not expired")
	require.False(t, plan.diffAllowed, "and the cooldown has not either, so a busy pane waits")

	require.Less(t, diffCooldownSelected, diffMaxAgeSelected,
		"the cooldown has to be the tighter of the two, or it can never open first")
	require.Less(t, diffCooldownOther, diffMaxAgeOther)
}

// The untracked walk is the expensive half of a cheap diff — 12.1ms against the
// 4–11ms of the diff itself — and a file appearing untracked is rare next to the
// rate this runs at.
func TestPlanMetadataPaysForTheUntrackedWalkOnItsOwnClock(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)

	require.True(t, h.planMetadata([]*session.Instance{inst})[0].stageUntracked,
		"never walked, so the first diff has to")

	inst.MarkUntrackedStaged()
	require.False(t, h.planMetadata([]*session.Instance{inst})[0].stageUntracked)
	require.Greater(t, untrackedMaxAge, diffMaxAgeOther,
		"the walk has to be rarer than the counts it feeds, or it saves nothing")
}

// Killing a session destroys its worktree and its branch, but not the Claude
// conversation that was happening in it -- and recovering that conversation is
// the whole point of the restore list. The record has to be written while the
// session still knows which directory it ran in: afterwards there is nothing
// left to find the transcript with.
func TestKilledSessionIsRecordedWithItsConversation(t *testing.T) {
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	workdir := t.TempDir()
	projectDir, err := resume.ProjectDir(workdir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "conversation-id.jsonl"),
		[]byte(`{"type":"user","sessionId":"conversation-id","cwd":"`+workdir+
			`","gitBranch":"TASK-9","message":{"role":"user","content":"land the migration"}}`+"\n"), 0o644))

	instance, err := session.FromInstanceData(session.InstanceData{
		Title:      "TASK-9",
		Path:       workdir,
		Status:     session.Paused,
		NoWorktree: true,
		Program:    "claude --dangerously-skip-permissions",
	})
	require.NoError(t, err)

	state := &fakeAppState{}
	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		errBox:    ui.NewErrBox(),
		list:      ui.NewList(&spin, false),
		appState:  state,
		program:   "claude",
		devStack:  dev.New(nil),
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(dev.New(nil))),
	}

	h.recordKilledSession(instance)

	require.Len(t, state.killed, 1)
	record := state.killed[0]
	assert.Equal(t, "TASK-9", record.Title)
	assert.Equal(t, "conversation-id", record.SessionID)
	assert.Equal(t, "land the migration", record.Summary)
	assert.Equal(t, "TASK-9", record.Branch, "the branch is read back from the transcript")

	// Restoring is a NEW session -- new worktree, new branch -- whose only
	// inheritance is the conversation, carried by --resume. The program's own
	// flags have to survive that, or a restored session comes back configured
	// differently from the one it replaces.
	_, cmd := h.restoreSession(record)
	require.NotNil(t, cmd)
	require.Equal(t, 1, h.list.NumInstances())
	restored := h.list.GetInstances()[0]
	assert.Equal(t, "claude --dangerously-skip-permissions --resume conversation-id", restored.Program)
	assert.Equal(t, "TASK-9", restored.Title)
}

// Two sessions cannot share a title: it names the tmux session and the branch,
// so tmux refuses the second one outright.
func TestRestoringTwiceGivesTheSecondSessionItsOwnName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		errBox:    ui.NewErrBox(),
		list:      ui.NewList(&spin, false),
		appState:  &fakeAppState{},
		program:   "claude",
		devStack:  dev.New(nil),
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(dev.New(nil))),
	}

	record := config.KilledSession{Title: "TASK-9", RepoPath: t.TempDir(), Program: "claude"}
	h.restoreSession(record)
	h.restoreSession(record)

	require.Equal(t, 2, h.list.NumInstances())
	assert.Equal(t, "TASK-9", h.list.GetInstances()[0].Title)
	assert.Equal(t, "TASK-9-2", h.list.GetInstances()[1].Title)

	// No transcript on disk, so nothing is resumed: the session comes back as the
	// same program on a fresh branch rather than with a session id that would
	// make claude refuse to start.
	assert.Equal(t, "claude", h.list.GetInstances()[0].Program)
}

// The bell is for the moment a session starts wanting you, not for every tick it
// goes on wanting you: announced on the transition, once.
func TestAttentionEventFiresOnTheTransitionOnly(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "s", Path: ".", Program: "claude"})
	require.NoError(t, err)

	step := func(a session.Activity) string {
		before, wasDone := inst.GetActivity(), inst.DoneAt()
		inst.SetActivity(a, 0)
		return attentionEvent(inst, before, wasDone)
	}

	require.Equal(t, "", step(session.ActivityIdle), "idle at startup is not news")
	require.Equal(t, "", step(session.ActivityWorking))
	require.Equal(t, "needs-input", step(session.ActivityNeedsInput))
	require.Equal(t, "", step(session.ActivityNeedsInput), "still waiting is not a second event")
	require.Equal(t, "", step(session.ActivityWorking), "answering it is not an event")
	require.Equal(t, "finished", step(session.ActivityIdle))
	require.Equal(t, "", step(session.ActivityIdle), "still idle is not a second event")
}

func TestOrphanedWorktreesIgnoresTheOnesSessionsOwn(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"owned_1", "feat/nested_2", "stray_3", ".hidden"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0755))
	}
	own := func(path string) *session.Instance {
		inst, err := session.FromInstanceData(session.InstanceData{
			Title: filepath.Base(path), Path: t.TempDir(), Status: session.Paused, Program: "claude",
			Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: path},
		})
		require.NoError(t, err)
		return inst
	}
	instances := []*session.Instance{own(filepath.Join(dir, "owned_1")), own(filepath.Join(dir, "feat", "nested_2"))}

	require.Equal(t, []string{"stray_3"}, orphanedWorktrees(dir, instances))
}

// An earlier message's timer must not clear a newer message.
func TestAnOlderMessageTimerLeavesANewerMessageAlone(t *testing.T) {
	h := &home{ctx: context.Background(), errBox: ui.NewErrBox()}
	_ = h.handleError(fmt.Errorf("first"))
	firstSeq := h.errSeq
	_ = h.handleNotice("second")

	h.Update(hideErrMsg{seq: firstSeq})
	require.Contains(t, h.errBox.String(), "second")
	h.Update(hideErrMsg{seq: h.errSeq})
	require.NotContains(t, h.errBox.String(), "second")
}

// Esc in the branch picker reached by tab from the name prompt goes back to the
// name prompt with the name kept. It used to kill the new session outright.
func TestEscFromTheBranchPickerReturnsToNaming(t *testing.T) {
	spin := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		menu:      ui.NewMenu(),
		errBox:    ui.NewErrBox(),
		list:      ui.NewList(&spin, false),
		appState:  &fakeAppState{},
		program:   "claude",
		devStack:  dev.New(nil),
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(dev.New(nil))),
	}
	// keySent skips the menu-highlight pass, which re-sends the key through the
	// update loop instead of handling it.
	press := func(msg tea.KeyMsg) { h.keySent = true; h.handleKeyPress(msg) }

	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	require.Equal(t, stateNew, h.state)
	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix")})
	press(tea.KeyMsg{Type: tea.KeyTab})
	require.Equal(t, statePrompt, h.state)
	press(tea.KeyMsg{Type: tea.KeyEsc})

	require.Equal(t, stateNew, h.state, "back at the name prompt")
	require.Equal(t, 1, h.list.NumInstances(), "the session being named survives")
	require.Equal(t, "fix", h.list.GetSelectedInstance().Title)

	press(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, 0, h.list.NumInstances(), "a second esc abandons it, as before")
}

func TestKillDetailSaysWhatGoes(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "s", Path: ".", Program: "claude"})
	require.NoError(t, err)

	inst.SetCIStatus(ci.Status{State: ci.StateMerged, PRNumber: 4773})
	require.Contains(t, killDetail(inst), "#4773 is merged")

	inst.SetCIStatus(ci.Status{State: ci.StateSuccess, PRNumber: 4773})
	inst.SetDiffStats(&git.DiffStats{Added: 120, Removed: 4}, false)
	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/s", Ahead: 3})
	detail := killDetail(inst)
	require.Contains(t, detail, "+120 −4")
	require.Contains(t, detail, "3 commit(s) not pushed")
}

// The whole interface at sizes smaller than its own chrome: a terminal that is
// still opening reports these for its first frame or two. None may panic.
func TestTheViewSurvivesATerminalSmallerThanItsChrome(t *testing.T) {
	for _, w := range []int{0, 1, 2, 5, 12, 40} {
		for _, hgt := range []int{0, 1, 2, 3, 4, 5, 6} {
			require.NotPanics(t, func() {
				h := newSizedHome(t, w, hgt)
				for _, state := range []state{stateDefault, stateConfirm, stateHelp} {
					h.state = state
					h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill?")
					h.textOverlay = overlay.NewTextOverlay("Instance Created")
					_ = h.View()
				}
			}, "%dx%d", w, hgt)
		}
	}
}

// A resume recreates the worktree and starts tmux -- git and tmux, each able to
// wait on a lock another process holds. It ran inside the key handler, so the
// interface could neither draw nor read a key until it finished, and ctrl+c had
// to be pressed over and over before one was noticed. The press now hands the
// work to a Cmd and returns at once, and a second press while it runs starts
// nothing.
func TestResumeRunsOffTheUpdateLoop(t *testing.T) {
	// Pre-fix this test resumed for real; keep that off the user's tmux server.
	dir, err := os.MkdirTemp("/tmp", "atmx")
	require.NoError(t, err)
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})

	h := newSizedHome(t, 150, 40)
	h.appState = &fakeAppState{}
	selected := h.list.GetSelectedInstance()
	require.True(t, selected.Paused())

	press := func() tea.Cmd {
		h.keySent = true // past the menu highlight, straight to the action
		_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		return cmd
	}
	require.NotNil(t, press(), "the resume must come back as a Cmd")
	require.True(t, selected.Paused(), "the key handler must not have resumed the session itself")
	require.True(t, h.busy[selected])

	press()
	require.True(t, h.busy[selected], "a second press must not start a second resume")

	// And a checkout of a session that is already paused starts nothing at all.
	other, err := session.FromInstanceData(session.InstanceData{
		Title: "checked-out", Path: t.TempDir(), Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: t.TempDir(), WorktreePath: t.TempDir(),
			SessionName: "checked-out", BranchName: "checked-out"}})
	require.NoError(t, err)
	h.list.AddInstance(other)()
	h.list.SelectInstance(other)
	h.keySent = true
	_, _ = h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	require.False(t, h.busy[other], "checking out a paused session must not start a pause")

	// The result arriving is what clears it.
	h.Update(instanceResumedMsg{instance: selected, err: errors.New("boom")})
	require.False(t, h.busy[selected])
}

// n then tab opens the branch picker, where "No branch" starts a session with no
// worktree. The overlay's prompt box was sized to 40% of the screen with every
// other field stacked on top of that, so below about 50 rows the picker and the
// Enter button were drawn off the bottom of the terminal: at 24 rows the option
// could not be seen at all. The overlay now fits the screen, the prompt box
// taking whatever is left.
func TestNewThenTabShowsTheWholeBranchPicker(t *testing.T) {
	for _, size := range [][2]int{{120, 24}, {150, 30}, {227, 54}} {
		h := newSizedHome(t, size[0], size[1])
		h.appState = &fakeAppState{}
		_, _ = h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		_, _ = h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		h.keySent = true // tab highlights its menu entry first and re-sends itself
		_, _ = h.handleKeyPress(tea.KeyMsg{Type: tea.KeyTab})
		require.Equal(t, statePrompt, h.state, "%dx%d: tab should open the branch picker", size[0], size[1])
		h.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: size[0], Height: size[1]})

		view := h.View()
		require.Len(t, strings.Split(view, "\n"), size[1], "%dx%d", size[0], size[1])
		require.Contains(t, view, "No branch (run in the repo itself)", "%dx%d", size[0], size[1])
		require.Contains(t, view, " Enter ", "%dx%d: the submit button must be on screen", size[0], size[1])
	}
}
