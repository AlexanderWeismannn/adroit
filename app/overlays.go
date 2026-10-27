package app

import (
	"fmt"

	"strings"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	tea "github.com/charmbracelet/bubbletea"
)

// An overlayScreen is drawn over the main view and owns the keyboard while it
// is open. One is open at a time -- home.overlay, nil when none -- and a screen
// closes itself by setting that field to nil. There is no second flag saying
// which screen is open, so nothing can fall out of step with the field that
// every reader checks.
type overlayScreen interface {
	// handleKey takes every key while the screen is open.
	handleKey(m *home, msg tea.KeyMsg) tea.Cmd
	// render draws the screen; the caller composites it over the main view.
	render(m *home) string
	// resize is told the terminal's size on every change. A screen that sizes
	// itself from the terminal does so here; the others ignore it.
	resize(m *home, width, height int)
}

// menuToDefault puts the menu back once the next frame has been measured, which
// is how every overlay used to close: the re-measure first, then the menu, in a
// Sequence so the order holds.
func (m *home) menuToDefault() tea.Cmd {
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// ---- prompt: the text input with its profile and branch pickers ----

// promptScreen is the prompt overlay: a new session's first prompt, the branch
// picker reached by tab from the name, or `i` sending to a running session.
type promptScreen struct {
	input *overlay.TextInputOverlay
}

// promptInput is the open prompt's input, or nil when no prompt is open. The
// branch search messages need it: a result that arrives after the prompt has
// closed is dropped.
func (m *home) promptInput() *overlay.TextInputOverlay {
	if s, ok := m.overlay.(*promptScreen); ok {
		return s.input
	}
	return nil
}

func (s *promptScreen) resize(m *home, width, height int) {
	r := ui.Layout(width, height, ui.Split)
	s.input.SetSize(r.Overlay.W, r.Overlay.H)
	s.input.SetMaxHeight(height)
}

func (s *promptScreen) render(m *home) string { return s.input.Render() }

func (s *promptScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	// Handle cancel via ctrl+c before delegating to the overlay
	if msg.String() == "ctrl+c" {
		return m.cancelPromptOverlay()
	}

	// Use the new TextInputOverlay component to handle all key events
	shouldClose, branchFilterChanged := s.input.HandleKeyPress(msg)

	// Check if the form was submitted or canceled
	if shouldClose {
		selected := m.list.GetSelectedInstance()
		if selected == nil {
			return nil
		}

		if s.input.IsCanceled() {
			// Reached by tab from the name prompt, esc steps back to it with
			// the name kept. It used to throw the whole new session away.
			if m.branchFromNaming && !selected.Started() {
				m.branchFromNaming = false
				m.overlay = nil
				m.naming = true
				m.menu.SetState(ui.StateNewInstance)
				m.syncNaming(true)
				return tea.WindowSize()
			}
			return m.cancelPromptOverlay()
		}
		m.branchFromNaming = false

		if s.input.IsSubmitted() {
			prompt := s.input.GetValue()
			selectedBranch := s.input.GetSelectedBranch()
			selectedProgram := s.input.GetSelectedProgram()
			noWorktree := s.input.IsNoWorktreeSelected()

			// A session reached by tabbing straight to the branch picker has no
			// name yet, and what it is opening is the name anyone would have
			// typed.
			if !selected.Started() && selected.Title == "" {
				var title string
				var err error
				if noWorktree {
					title = titleForRepo(selected.Path, m.list.GetInstances())
				} else {
					title, err = titleForBranch(selectedBranch, m.list.GetInstances())
				}
				if err == nil {
					err = selected.SetTitle(title)
				}
				if err != nil {
					// The message goes in the overlay, not the error box. The
					// overlay covers most of the screen and fades the rest, so an
					// error behind it reads as nothing having happened.
					s.input.Submitted = false
					s.input.SetError(err.Error())
					s.input.FocusBranchPicker()
					return nil
				}
			}

			if !selected.Started() {
				// Shift+N flow: instance not started yet — set branch, start, then send prompt
				if selectedBranch != "" {
					selected.SetSelectedBranch(selectedBranch)
				}
				selected.SetNoWorktree(noWorktree)
				if selectedProgram != "" {
					selected.Program = selectedProgram
				}
				selected.Prompt = prompt

				// Finalize into list and start
				selected.SetStatus(session.Loading)
				m.newInstanceFinalizer()
				m.overlay = nil
				m.menu.SetState(ui.StateDefault)

				startCmd := func() tea.Msg {
					err := selected.Start(true)
					return instanceStartedMsg{
						instance:        selected,
						err:             err,
						promptAfterName: false,
						selectedBranch:  selectedBranch,
					}
				}

				return tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
			}

			// Instance already running (the `i` key): send the prompt and go
			// straight back to the list. Off the loop, since sending pauses
			// between the text and the enter.
			m.overlay = nil
			m.menu.SetState(ui.StateDefault)
			if strings.TrimSpace(prompt) == "" {
				return tea.WindowSize()
			}
			title := selected.Title
			return tea.Batch(tea.WindowSize(), func() tea.Msg {
				if err := selected.SendPrompt(prompt); err != nil {
					return err
				}
				return noticeMsg(fmt.Sprintf("sent to %s", title))
			})
		}

		// Close the overlay and reset state
		m.overlay = nil
		return tea.Sequence(
			tea.WindowSize(),
			func() tea.Msg {
				m.menu.SetState(ui.StateDefault)
				m.showHelpScreen(helpStart(selected), nil)
				return nil
			},
		)
	}

	// Schedule a debounced branch search if the filter changed
	if branchFilterChanged {
		filter := s.input.BranchFilter()
		version := s.input.BranchFilterVersion()
		return m.scheduleBranchSearch(filter, version)
	}

	return nil
}

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
	m.overlay = nil
	return m.menuToDefault()
}

// ---- help: a card of text, dismissed by any key ----

type helpScreen struct {
	text *overlay.TextOverlay
}

func (s *helpScreen) resize(m *home, width, height int) {
	s.text.SetWidth(ui.Layout(width, height, ui.Split).Overlay.W)
}

func (s *helpScreen) render(m *home) string { return s.text.Render() }

func (s *helpScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	// Any key press will close the help overlay
	shouldClose, dismissed := s.text.HandleKeyPress(msg)
	if shouldClose {
		m.overlay = nil
		// Set outright rather than from inside a Cmd. We are already in Update
		// with the model in hand, and the ordering the Cmd bought -- menu reset
		// after re-measure -- never meant anything for a state flag.
		m.menu.SetState(ui.StateDefault)

		// What the help screen was gating goes back on its own rather than inside
		// a tea.Sequence. It can be an attach, which reaches bubbletea as an
		// exec: a Sequence runs on its own goroutine and hands each result to
		// the event loop one at a time, which is not a useful thing to put an
		// exec in the middle of. Nothing is lost by dropping the re-measure on
		// this path -- an attach re-measures when it finishes, and the other
		// callback ends in instanceChanged.
		if dismissed != nil {
			return dismissed
		}

		return tea.WindowSize()
	}

	return nil
}

// ---- confirm: a yes/no dialog holding the action it guards ----

type confirmScreen struct {
	dialog *overlay.ConfirmationOverlay
	// action runs once the dialog is confirmed, as a Cmd: it reaches the model
	// only through the message it returns, so it is safe on a Cmd's goroutine
	// -- and must be, because run on the loop it would block every keystroke
	// and every frame until it finished (a kill tears down a tmux session, a
	// git worktree and the dev stack, none of which is bounded).
	action    tea.Cmd
	confirmed bool
}

func (s *confirmScreen) resize(m *home, width, height int) {
	s.dialog.SetWidth(ui.Layout(width, height, ui.Split).Dialog.W)
}

func (s *confirmScreen) render(m *home) string { return s.dialog.Render() }

func (s *confirmScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	shouldClose := s.dialog.HandleKeyPress(msg)
	if shouldClose {
		m.overlay = nil
		// Run whatever was confirmed, off the loop. Whatever it returns -- an
		// error to show, or a step that does touch the model -- comes back
		// through Update like any other message.
		if s.confirmed {
			s.confirmed = false
			return s.action
		}
	}
	return nil
}

// confirmAction shows a yes/no dialog -- title in the border, message as the
// body saying what is lost and what can be recovered, verb as the word beside
// y -- and stores the action to run on confirm.
func (m *home) confirmAction(title, message, verb string, action tea.Cmd) tea.Cmd {
	s := &confirmScreen{dialog: overlay.NewConfirmationOverlay(message), action: action}
	s.dialog.Title = title
	s.dialog.ConfirmLabel = verb
	s.dialog.SetWidth(m.layout.Dialog.W)
	s.dialog.OnConfirm = func() { s.confirmed = true }
	s.dialog.OnCancel = func() { s.confirmed = false }
	m.overlay = s
	return nil
}

// ---- theme: the picker, previewing as the cursor moves ----

type themeScreen struct {
	picker *overlay.ThemePicker
	// before is the palette that was in use when the picker opened, kept here
	// rather than in the picker so cancelling restores it from the one place
	// that installed it.
	before theme.Palette
}

func (s *themeScreen) resize(m *home, width, height int) {}

func (s *themeScreen) render(m *home) string { return s.picker.Render() }

// handleKey drives the theme picker: every move installs the highlighted
// palette so the interface behind the overlay is the preview, enter keeps it, and
// escape puts back what was there.
func (s *themeScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	// ctrl+c is cancel here rather than quit, matching the other overlays: the
	// picker has changed what the user is looking at, and leaving cs in a theme
	// they were only previewing would be a surprising way to exit.
	if msg.String() == "ctrl+c" {
		return s.close(m, true)
	}

	switch s.picker.HandleKeyPress(msg) {
	case overlay.PickerPreview:
		m.previewTheme(s.picker.Selected())
		return nil
	case overlay.PickerCancel:
		return s.close(m, true)
	case overlay.PickerConfirm:
		selected := s.picker.Selected()
		changed := s.picker.Changed()
		cmd := s.close(m, false)
		if !changed {
			return cmd
		}
		// The palette is already installed by the preview, so a failed write
		// leaves the interface showing a theme the config does not have. That is
		// the right way round: the user sees what they chose and is told it could
		// not be saved, rather than having their choice silently reverted.
		m.appConfig.Theme = selected
		if err := config.UpdateConfigFile(map[string]any{"theme": selected}); err != nil {
			return tea.Batch(cmd, m.handleError(fmt.Errorf("theme applied, but not saved: %w", err)))
		}
		return cmd
	}
	return nil
}

// previewTheme installs a theme together with the user's own colour overrides,
// so the preview is what they will actually get rather than the theme's own
// values for roles they have replaced.
func (m *home) previewTheme(name string) {
	palette, _ := theme.Resolve(name, m.appConfig.Colors)
	theme.Set(palette)
}

// close leaves the picker, restoring the previous palette when the choice was
// abandoned.
func (s *themeScreen) close(m *home, restore bool) tea.Cmd {
	if restore && s.before != nil {
		theme.Set(s.before)
	}
	m.overlay = nil
	return m.menuToDefault()
}

// ---- restore: the killed sessions whose conversation can be picked back up ----

type restoreScreen struct {
	picker *overlay.RestorePicker
}

func (s *restoreScreen) resize(m *home, width, height int) {}

func (s *restoreScreen) render(m *home) string { return s.picker.Render() }

// handleKey drives the picker: moving changes nothing but the cursor, enter
// starts the chosen session, and every way out leaves the list untouched.
func (s *restoreScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	// ctrl+c closes the picker rather than quitting, matching the other overlays.
	if msg.String() == "ctrl+c" {
		return s.close(m)
	}

	switch s.picker.HandleKeyPress(msg) {
	case overlay.PickerCancel:
		return s.close(m)
	case overlay.PickerConfirm:
		entry, ok := s.picker.Selected()
		if !ok {
			return s.close(m)
		}
		record, found := m.killedSessionFor(entry)
		closeCmd := s.close(m)
		if !found {
			return tea.Batch(closeCmd, m.handleError(fmt.Errorf("could not find %q to restore", entry.Title)))
		}
		_, restoreCmd := m.restoreSession(record)
		return tea.Batch(closeCmd, restoreCmd)
	}
	return nil
}

func (s *restoreScreen) close(m *home) tea.Cmd {
	m.overlay = nil
	return m.menuToDefault()
}

// ---- settings: agents, keys, workspaces, options ----

type settingsScreen struct {
	settings *overlay.Settings
}

func (s *settingsScreen) resize(m *home, width, height int) {}

func (s *settingsScreen) render(m *home) string {
	s.settings.SetWidth(m.termWidth)
	return s.settings.Render()
}

func (s *settingsScreen) handleKey(m *home, msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		s.close(m)
		return nil
	}
	cmd, closed := s.settings.HandleKeyPress(msg, checkAgentKey)
	if closed {
		s.close(m)
	}
	return cmd
}

func (s *settingsScreen) close(m *home) {
	m.overlay = nil
	m.menu.SetState(ui.StateDefault)
}
