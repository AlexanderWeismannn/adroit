package app

import (
	"fmt"
	"os"
	"time"

	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *home) handleMenuHighlighting(msg tea.KeyPressMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	// The overlays own every key while they are open; highlighting a menu entry
	// underneath one, and re-sending the key to do it, is both invisible and a
	// second delivery of a keypress the overlay already handled.
	if m.overlay != nil {
		return nil, false
	}
	// While a name is being typed the row owns every printable key. Most letters
	// are also shortcuts here -- n, r, t, d, x, p, c, g, o -- so without this,
	// typing "post-ai-svg" highlighted a different menu entry on nearly every
	// keystroke and re-sent each one through the update loop to get it delivered.
	// Enter, tab and escape still highlight: they are the ones the menu is
	// advertising while the row is being named.
	if m.naming {
		if msg.Text != "" || msg.Code == tea.KeySpace || msg.Code == tea.KeyBackspace {
			return nil, false
		}
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp || name == keys.KeyPageUp || name == keys.KeyPageDown {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter while naming, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.naming {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyPressMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	// An open screen owns every key.
	if m.overlay != nil {
		return m, m.overlay.handleKey(m, msg)
	}

	if m.naming {
		// The preview follows every keystroke, and is cleared by every way out.
		defer func() {
			m.syncNaming(m.naming)
		}()

		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.naming = false
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.RequestWindowSize,
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Code {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}
			if err := titleCollision(instance, m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}

			// If promptAfterName, show prompt+branch overlay before starting
			if m.promptAfterName {
				m.promptAfterName = false
				m.naming = false
				m.menu.SetState(ui.StatePrompt)
				input := m.newPromptOverlay()
				m.overlay = &promptScreen{input: input}
				// Trigger initial branch search (no debounce, version 0)
				initialSearch := m.runBranchSearch("", input.BranchFilterVersion())
				return m, tea.Batch(tea.RequestWindowSize, initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.naming = false
			m.menu.SetState(ui.StateDefault)

			// Return a tea.Cmd that runs instance.Start in the background
			startCmd := func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}

			return m, tea.Batch(tea.RequestWindowSize, m.instanceChanged(), startCmd)
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		default:
			if !overlay.IsTyped(msg) {
				return m, nil
			}
			if ansi.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + msg.Text); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyTab:
			// The branch picker used to be reachable only through N ("new with
			// prompt"), whose name says nothing about branches -- so the ordinary
			// way to start a session offered no way to start it on an existing
			// one. Tab was a no-op here, so this costs nothing: enter still
			// creates on a new branch in the same two keystrokes as before, and
			// tab is the opt-in for anyone who wants to choose.
			//
			// It opens the same overlay N does rather than a second branch-only
			// variant, focused on the picker instead of the prompt. The prompt may
			// be left blank -- an empty one is never sent.
			//
			// The name may be blank too: when you are opening an existing branch
			// the branch name is the name you would have typed, so requiring one
			// first is pure friction. It is filled in from the branch on submit.
			m.promptAfterName = false
			m.branchFromNaming = true
			m.naming = false
			m.menu.SetState(ui.StatePrompt)
			input := m.newBranchOverlay()
			m.overlay = &promptScreen{input: input}
			version := input.BranchFilterVersion()
			initialSearch := m.runBranchSearch("", version)
			// Fetch as N does, then search again: this path listed only the
			// branches already known locally, so a branch pushed since the last
			// fetch was missing from one of the two ways in and not the other. A
			// filter typed meanwhile moves the version on and the stale result is
			// dropped.
			refreshed := tea.Sequence(func() tea.Msg {
				currentDir, _ := os.Getwd()
				git.FetchBranches(currentDir)
				return nil
			}, m.runBranchSearch("", version))
			return m, tea.Batch(tea.RequestWindowSize, initialSearch, refreshed)
		case tea.KeyEscape:
			m.list.Kill()
			m.naming = false
			m.instanceChanged()

			return m, tea.Sequence(
				tea.RequestWindowSize,
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Code == tea.KeyEscape {
		// If in preview tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInPreviewTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			// Use the selected instance from the list
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in terminal tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInTerminalTab() && m.tabbedWindow.IsTerminalInScrollMode() {
			m.tabbedWindow.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
		if m.tabbedWindow.IsInRunTab() && m.tabbedWindow.IsRunInScrollMode() {
			m.tabbedWindow.ResetRunToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	// 1-9 select the row wearing that number. Not in the key map: nine bindings
	// for one action would crowd the menu, and the numbers are already on screen.
	if k := msg.String(); len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		if idx := int(k[0] - '1'); idx < m.list.NumInstances() {
			m.list.SetSelectedInstance(idx)
			return m, m.instanceChanged()
		}
		return m, nil
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	switch name {
	case keys.KeyNextAttention:
		if !m.list.SelectNextAttention() {
			return m, m.handleNotice("nothing else is waiting on you")
		}
		return m, m.instanceChanged()
	case keys.KeySendPrompt:
		selected := m.list.GetSelectedInstance()
		if selected == nil || !selected.Started() || selected.Status == session.Paused ||
			selected.Status == session.Loading {
			return m, nil
		}
		m.menu.SetState(ui.StatePrompt)
		m.overlay = &promptScreen{input: overlay.NewTextInputOverlay(fmt.Sprintf("Send to %s", selected.Title), "")}
		return m, tea.RequestWindowSize
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyPrompt:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}

		// Start a background fetch so branches are up to date by the time the picker opens
		fetchCmd := func() tea.Msg {
			currentDir, _ := os.Getwd()
			git.FetchBranches(currentDir)
			return nil
		}

		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.newSessionProgram(),
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.naming = true
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, fetchCmd
	case keys.KeyNew:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:   "",
			Path:    ".",
			Program: m.newSessionProgram(),
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.naming = true
		m.menu.SetState(ui.StateNewInstance)
		m.syncNaming(true)

		return m, nil
	case keys.KeyUp:
		if m.windowFocused {
			m.tabbedWindow.ScrollUp()
			return m, m.instanceChanged()
		}
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		if m.windowFocused {
			m.tabbedWindow.ScrollDown()
			return m, m.instanceChanged()
		}
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyPageUp:
		m.tabbedWindow.PageUp()
		return m, m.instanceChanged()
	case keys.KeyPageDown:
		m.tabbedWindow.PageDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.setWindowFocus(!m.windowFocused)
		return m, m.instanceChanged()
	case keys.KeyMode:
		return m, m.cycleMode()
	case keys.KeyPrevSession:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyNextSession:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyLeft:
		m.tabbedWindow.Prev()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyRight:
		m.tabbedWindow.Toggle()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if m.busy[selected] {
			return m, m.handleNotice(fmt.Sprintf("%q is still being resumed or checked out", selected.Title))
		}

		// The confirmed action does the checks only. Everything it approves is
		// carried back through killApprovedMsg, because the rest of a kill either
		// touches the model or blocks, and the two need opposite treatment.
		killAction := func() tea.Msg {
			// A session running in the repository itself has no worktree to remove
			// and no branch of its own to protect, so there is nothing to check --
			// only the tmux session and our record of it to tear down. Asking for the
			// worktree anyway returns an error, and killing it would then do nothing
			// at all: the session would survive the confirmation, and survive a
			// restart too, because storage is written further down.
			if !selected.NoWorktree() {
				// Get worktree and check if branch is checked out
				worktree, err := selected.GetGitWorktree()
				if err != nil {
					return err
				}

				checkedOut, err := worktree.IsBranchCheckedOut()
				if err != nil {
					return err
				}

				if checkedOut {
					return fmt.Errorf("instance %s is currently checked out", selected.Title)
				}
			}

			return killApprovedMsg{instance: selected}
		}

		// Show confirmation modal
		message := fmt.Sprintf("Kill %s?", selected.Title)
		if detail := killDetail(selected); detail != "" {
			message += "\n\n" + detail
		}
		return m, m.confirmAction("Kill session", message, "kill", killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		// Nothing of this session's own to push: its changes are the user's own
		// working tree, and committing that on their behalf is not what p means.
		if selected.NoWorktree() {
			return m, m.handleError(fmt.Errorf("%q runs in the repo itself — commit and push it yourself", selected.Title))
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[adroit] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("Commit everything in %s's worktree and push its branch?", selected.Title)
		return m, m.confirmAction("Push", message, "push", pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		// Checkout commits the worktree and removes it. There is no worktree here,
		// and doing that to the user's own checkout is emphatically not the intent.
		if selected.NoWorktree() {
			return m, m.handleError(fmt.Errorf("%q runs in the repo itself — there is no worktree to check out", selected.Title))
		}
		// Already checked out: another pause would only fail, after announcing
		// that it was checking the session out.
		if selected.Paused() {
			return m, m.handleNotice(fmt.Sprintf("%q is already checked out: press r to resume it", selected.Title))
		}

		// Show help screen before pausing. Its result is returned, not dropped:
		// with the help already seen the pause is started right here, and
		// dropping the Cmd discarded the pause's error along with it.
		return m.showHelpScreen(helpTypeInstanceCheckout{}, func() tea.Cmd {
			return m.offLoop(selected, "checking out", func() tea.Msg {
				return instancePausedMsg{instance: selected, err: selected.Pause()}
			})
		})
	case keys.KeyUpdate:
		return m, m.updateFromRemote()
	case keys.KeyDev:
		return m, m.startDevStack()
	case keys.KeyDevStop:
		return m, m.stopDevStack()
	case keys.KeyRestore:
		return m.openRestorePicker()
	case keys.KeySettings:
		return m.openSettings()
	case keys.KeyTheme:
		m.overlay = &themeScreen{picker: overlay.NewThemePicker(m.appConfig.Theme), before: theme.Current()}
		return m, nil
	case keys.KeyOpenPR:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		// The key is drawn dim when no pull request is known, and a press on it
		// gets the reason rather than gh's error after a round trip. Only when
		// the lookup is on: switched off, nothing is known either way and gh is
		// the one that can say.
		if m.appConfig.CIStatusEnabled() && selected.GetCIStatus().PRNumber == 0 {
			return m, m.handleNotice(fmt.Sprintf("no pull request known for %s yet — push it with p, then g opens it", selected.Title))
		}
		worktree, err := selected.GetGitWorktree()
		if err != nil {
			return m, m.handleError(err)
		}
		// The branch the worktree actually has checked out, matching what the row
		// shows and what the badge resolved its verdict from.
		repoPath, branch := worktree.GetRepoPath(), selected.DisplayBranch()
		// Off the event loop: gh blocks until the browser command returns, and the
		// menu only advertises the key when a pull request is known to exist, so a
		// press that reaches gh with none is worth the error it comes back with.
		return m, func() tea.Msg {
			if err := ci.OpenInBrowser(repoPath, branch); err != nil {
				return err
			}
			return nil
		}
	case keys.KeyMoveUp:
		if m.list.MoveUp() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyMoveDown:
		if m.list.MoveDown() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		// A resume recreates the worktree (git, which waits on any lock another
		// process holds) and starts tmux; run inline, the interface could neither
		// draw nor read a key until it was done, and ctrl+c had to be pressed
		// again and again to be noticed at all.
		return m, m.offLoop(selected, "resuming", func() tea.Msg {
			return instanceResumedMsg{instance: selected, err: selected.Resume()}
		})
	case keys.KeyEnter:
		// Run tab: attach to the stack's own pane, wherever it is running. Not
		// gated on the selected session, unlike every other attach here: the pane
		// belongs to the stack, and the row the cursor happens to be on has no
		// bearing on it. Checked first for that reason -- after the guard below,
		// a paused row under the cursor made the stack unreachable.
		if m.tabbedWindow.IsInRunTab() {
			return m.showHelpScreen(helpTypeInstanceAttach{}, func() tea.Cmd {
				return m.attachCmd(m.tabbedWindow.AttachRun)
			})
		}
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected != nil && selected.Paused() {
			return m, m.handleNotice(fmt.Sprintf("%q is paused: press r to resume it first", selected.Title))
		}
		if selected == nil || selected.Status == session.Loading || !selected.TmuxAlive() {
			return m, nil
		}
		// Terminal tab: attach to terminal session
		if m.tabbedWindow.IsInTerminalTab() {
			return m.showHelpScreen(helpTypeInstanceAttach{}, func() tea.Cmd {
				return m.attachCmd(m.tabbedWindow.AttachTerminal)
			})
		}
		// Show help screen before attaching
		// The re-measure that used to be here now happens on attachFinishedMsg.
		// It has to wait for the detach -- the session is resized to the real
		// terminal while attached, and the terminal itself may have changed shape
		// meanwhile, and neither produces a window-size event of its own -- and
		// this no longer blocks until then.
		return m.showHelpScreen(helpTypeInstanceAttach{}, func() tea.Cmd {
			return m.attachCmd(m.list.Attach)
		})
	default:
		return m, nil
	}
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// setWindowFocus hands the keyboard to the window or back to the list: the
// borders show which, and the bar says what the arrows do there.
func (m *home) setWindowFocus(window bool) {
	m.windowFocused = window
	m.list.SetFocused(!window)
	m.tabbedWindow.SetFocused(window)
	if window {
		m.menu.SetState(ui.StatePane)
	} else {
		m.menu.SetState(ui.StateDefault)
	}
}
