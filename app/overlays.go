package app

import (
	"fmt"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// newBranchOverlay is the same overlay reached by tabbing from the name prompt to
// choose a branch. It opens on the picker, and its title says the prompt is
// optional -- arriving here without a name, the textarea is the one field that
// looks like it might be asking for one.
func (m *home) newBranchOverlay() *overlay.TextInputOverlay {
	o := overlay.NewTextInputOverlayWithBranchPicker("Enter prompt (optional)", "", m.appConfig.GetProfiles())
	o.FocusBranchPicker()
	return o
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up unstarted instances.
func (m *home) cancelPromptOverlay() tea.Cmd {
	m.branchFromNaming = false
	selected := m.list.GetSelectedInstance()
	if selected != nil && !selected.Started() {
		m.list.Kill()
	}
	m.textInputOverlay = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Stored, not called. Actions reach the model only through the messages
		// they return, so each one is safe to run on a Cmd's goroutine -- and must
		// be, because running one here would block every keystroke and every frame
		// until it finished.
		m.pendingAction = action
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
		m.pendingAction = nil
	}

	return nil
}

// handleThemeState drives the theme picker: every move installs the highlighted
// palette so the interface behind the overlay is the preview, enter keeps it, and
// escape puts back what was there.
func (m *home) handleThemeState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c is cancel here rather than quit, matching the other overlays: the
	// picker has changed what the user is looking at, and leaving cs in a theme
	// they were only previewing would be a surprising way to exit.
	if msg.String() == "ctrl+c" {
		return m, m.closeThemePicker(true)
	}

	switch m.themePicker.HandleKeyPress(msg) {
	case overlay.PickerPreview:
		m.previewTheme(m.themePicker.Selected())
		return m, nil
	case overlay.PickerCancel:
		return m, m.closeThemePicker(true)
	case overlay.PickerConfirm:
		selected := m.themePicker.Selected()
		changed := m.themePicker.Changed()
		cmd := m.closeThemePicker(false)
		if !changed {
			return m, cmd
		}
		// The palette is already installed by the preview, so a failed write
		// leaves the interface showing a theme the config does not have. That is
		// the right way round: the user sees what they chose and is told it could
		// not be saved, rather than having their choice silently reverted.
		m.appConfig.Theme = selected
		if err := config.UpdateConfigFile(map[string]any{"theme": selected}); err != nil {
			return m, tea.Batch(cmd, m.handleError(fmt.Errorf("theme applied, but not saved: %w", err)))
		}
		return m, cmd
	}
	return m, nil
}

// previewTheme installs a theme together with the user's own colour overrides,
// so the preview is what they will actually get rather than the theme's own
// values for roles they have replaced.
func (m *home) previewTheme(name string) {
	palette, _ := theme.Resolve(name, m.appConfig.Colors)
	theme.Set(palette)
}

// closeThemePicker leaves the picker, restoring the previous palette when the
// choice was abandoned.
func (m *home) closeThemePicker(restore bool) tea.Cmd {
	if restore && m.themeBeforePicker != nil {
		theme.Set(m.themeBeforePicker)
	}
	m.themePicker = nil
	m.themeBeforePicker = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}
