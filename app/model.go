package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool) error {
	h := newHome(ctx, program, autoYes)
	p := tea.NewProgram(
		maybeProfiled(h),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),  // Mouse scroll
		tea.WithoutSignalHandler(), // see handleTermination
	)
	defer handleTermination(p, h.terminating)()
	_, err := p.Run()
	return err
}

// terminateMsg is delivered once a SIGTERM or SIGINT has arrived, and anything
// attached has been detached.
type terminateMsg struct{}

// handleTermination makes `kill` quit Adroit, and returns the function that
// stops listening.
//
// Bubbletea's own handler does not do this. It ignores signals whenever the
// terminal is released, which is the whole time a session is attached -- and
// attached is the state Adroit is usually left in when its terminal window is
// closed, since tmux keeps it running. So the one moment someone reaches for
// `kill` is the one moment the signal was thrown away, and the instance lock
// kept every later launch out. Its quit also skipped saving the instances.
//
// Closing terminating ends an attach in progress (see attachCommand.Run), which
// hands the event loop back so the quit can be delivered. A second signal gets
// the default action and kills the process outright: the way out if the quit
// itself is what has wedged.
func handleTermination(p *tea.Program, terminating chan struct{}) (stop func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case sig := <-sigs:
			log.InfoLog.Printf("received %v, quitting", sig)
		}
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		close(terminating)
		p.Send(terminateMsg{})
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

type home struct {
	// busy holds the sessions a resume or checkout is working on off the update
	// loop, so a second press of the same key -- or a kill -- cannot start on a
	// session that is still halfway through the first.
	busy map[*session.Instance]bool
	// startupNotice is shown once the interface is up: something found while
	// loading that is worth a look but not worth refusing to start over.
	startupNotice string
	// errSeq numbers the messages shown in the error box; see hideErrMsg.
	errSeq int
	// branchFromNaming is whether the prompt overlay was opened by tab from the
	// name prompt, which is where esc should return to.
	branchFromNaming bool
	ctx              context.Context
	// terminating is closed when a signal has told Adroit to quit.
	terminating chan struct{}

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// namingRepo is the repository a new session would be created in -- the
	// working directory's -- resolved once at startup. The branch prefix and the
	// case policy are per repository, and the branch is previewed on every
	// keystroke, which is no place to walk the tree looking for a .git.
	namingRepo string
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// overlay is the screen drawn over the main view, which owns the keyboard
	// while it is open; nil when none is. See overlayScreen.
	overlay overlayScreen
	// naming is true while a new session's name is being typed into its row.
	// Not an overlay: the row itself is the input.
	naming bool
	// newInstanceFinalizer is called when a session is being named and you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// layout is where every component goes for the current terminal size.
	layout ui.Rects
	// fitWarnedWidth is the last width at which fitToTerminal had to cut a row,
	// logged once per width rather than once per frame.
	fitWarnedWidth int
	// termWidth is the terminal's own width, kept so the composed frame can be
	// held to it. Nothing may be drawn wider than the screen: tmux clips the
	// overflow, and what it clips is the right-hand edge of the interface.
	termWidth int
	// spinnerRunning is whether the animation's tick chain is alive. The chain
	// ends when nothing on screen is animating, so something has to know to start
	// it again -- see spinnerNeeded.
	spinnerRunning bool
	// paneHash fingerprints the capture the active pane is currently showing,
	// so the tick can tell a pane that has not changed from one that has, and
	// paneChanged is when it last did, which sets the sampling pace.
	paneHash    uint64
	paneChanged time.Time
	// devStack owns the singleton development stack. Nil is impossible; an
	// unconfigured stack is a live object that reports itself unconfigured, so
	// every call site can ask rather than nil-check.
	devStack *dev.Stack

	// programOverridden is true when -p chose the program, which then wins over
	// a repository's own agent.
	programOverridden bool
}

func newHome(ctx context.Context, program string, autoYes bool) *home {
	// Load application config
	appConfig := config.LoadConfig()

	// Load application state
	appState := config.LoadState()

	// Initialize storage
	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	devStack := dev.New(appConfig.Dev)
	// Which stack runs a session is a question about its repository, not about
	// the process: one Adroit drives sessions from every repository you have
	// open, and `npm run dev` is an answer about exactly one of them.
	devStack.SetResolver(appConfig.DevFor)

	h := &home{
		ctx:     ctx,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:    ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(
			ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane(), ui.NewRunPane(devStack)),
		errBox:            ui.NewErrBox(),
		storage:           storage,
		appConfig:         appConfig,
		program:           program,
		programOverridden: program != appConfig.GetProgram(),
		autoYes:           autoYes,
		appState:          appState,
		devStack:          devStack,
		terminating:       make(chan struct{}),
	}
	h.list = ui.NewList(&h.spinner, autoYes)
	// New sessions are always created in the working directory, so the naming
	// rules the interface previews are that repository's -- resolved once here
	// rather than per keystroke. A directory that is not a repository leaves the
	// global rules in place; the creation itself will say so.
	h.namingRepo, _ = git.RepoRoot(".")
	h.list.SetBranchNaming(appConfig.BranchPrefixFor(h.namingRepo), appConfig.PreserveBranchCaseFor(h.namingRepo))
	h.menu.SetDevEnabled(devStack.Configured())
	h.menu.SetRestorable(len(appState.GetKilledSessions()) > 0)

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	reapOrphanedTerminals(instances)
	if dir, err := config.GetConfigDir(); err == nil {
		if orphans := orphanedWorktrees(filepath.Join(dir, "worktrees"), instances); len(orphans) > 0 {
			log.WarningLog.Printf("worktrees no session owns, left in place: %s", strings.Join(orphans, ", "))
			h.startupNotice = fmt.Sprintf("%d folder(s) in ~/.adroit/worktrees belong to no session: %s",
				len(orphans), strings.Join(orphans, ", "))
		}
	}

	// A config or state that could not be loaded was replaced by defaults. Said
	// first: it explains an empty list or a wrong branch prefix, which nothing
	// else on screen would.
	if warnings := config.TakeLoadWarnings(); len(warnings) > 0 {
		h.startupNotice = strings.Join(append(warnings, h.startupNotice), " · ")
		h.startupNotice = strings.TrimSuffix(h.startupNotice, " · ")
	}

	// A stack started by a previous run is still holding the ports, so adopt it
	// before anything tries to start another one.
	if title := appState.GetDevStackInstance(); title != "" {
		for _, instance := range instances {
			if instance.Title == title {
				devStack.Adopt(title, instance.DevDir(), instance.RepoPath())
				break
			}
		}
	}

	// Add loaded instances to the list
	for _, instance := range instances {
		// Call the finalizer immediately.
		h.list.AddInstance(instance)()
		if autoYes {
			instance.AutoYes = true
		}
	}

	return h
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	m.termWidth = msg.Width
	// Every component gets its rect from the layout; nothing measures the
	// terminal for itself. The rows add up exactly: a frame composed one row
	// taller than the terminal scrolls the whole view by one on every redraw.
	r := ui.Layout(msg.Width, msg.Height, ui.Split)
	m.layout = r
	m.errBox.SetSize(r.Notice.W, r.Notice.H)

	m.tabbedWindow.SetSize(r.Window.W, r.Window.H)
	m.list.SetSize(r.List.W, r.List.H)

	if m.overlay != nil {
		m.overlay.resize(m, msg.Width, msg.Height)
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(r.Bar.W, r.Bar.H)
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	m.spinnerRunning = true
	var notice tea.Cmd
	if m.startupNotice != "" {
		notice = m.handleNotice(m.startupNotice)
	}
	return tea.Batch(
		notice,
		m.spinner.Tick,
		m.paneTick(),
		tickUpdateMetadataCmd(m.planMetadata(m.snapshotActiveInstances()),
			m.appConfig.CIStatusEnabled(), m.appConfig.UpstreamStatusEnabled(), m.devStack),
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case terminateMsg:
		// Unlike q, a save that fails does not stop the quit: whoever sent the
		// signal is not looking at the error box.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			log.ErrorLog.Printf("could not save the sessions before quitting: %v", err)
		}
		return m, tea.Quit
	case attachFinishedMsg:
		// Back from a session. Both panes and the session itself may be the wrong
		// shape now: it was resized to the real terminal while attached, and the
		// terminal may have been resized while we were not watching.
		m.overlay = nil
		if msg.err != nil {
			return m, tea.Batch(tea.WindowSize(), m.handleError(msg.err))
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case hideErrMsg:
		// Only the timer of the message on screen may clear it. Every message
		// starts its own, so an earlier one's used to wipe a newer message early.
		if msg.seq == m.errSeq {
			m.errBox.Clear()
		}
	case paneTickMsg:
		return m, tea.Batch(m.applyPaneTick(msg), m.paneTick())
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case devStackStoppedMsg:
		m.syncDevBadge()
		if msg.err != nil {
			return m, tea.Batch(m.instanceChanged(), m.handleError(msg.err))
		}
		return m, m.instanceChanged()
	case devStackStartedMsg:
		if msg.err != nil {
			// The record is only true while the stack is up; a failed start must
			// not leave the next run adopting something that never existed.
			if err := m.appState.SetDevStackInstance(""); err != nil {
				log.WarningLog.Printf("could not clear the dev stack session: %v", err)
			}
			m.syncDevBadge()
			return m, tea.Batch(m.instanceChanged(), m.handleError(msg.err))
		}
		return m, m.instanceChanged()
	case metadataUpdateDoneMsg:
		var events []attention
		goneAny := false
		for _, r := range msg.results {
			// Skip instances that were paused while metadata was being computed
			if r.instance.Status == session.Paused {
				continue
			}
			// Where the agent's own interface can be read, believe it. The
			// content hash cannot see the case that matters: a session that has
			// ended its turn with a background shell still running holds a
			// perfectly still pane, and the hash calls that finished -- so the row
			// goes green and invites you to carry on, moments before the agent
			// wakes up and starts writing again.
			if r.pane.Gone {
				r.instance.MarkGone()
				goneAny = true
				continue
			}
			before, wasDone := r.instance.GetActivity(), r.instance.DoneAt()
			switch {
			// First, ahead of Working: a dialog at the bottom of the pane means the
			// turn is blocked on you whatever else the pane says.
			case r.pane.NeedsInput && !(r.pane.HasPrompt && r.instance.AutoYes):
				r.instance.SetActivity(session.ActivityNeedsInput, r.pane.BackgroundShells)
				r.instance.SetStatus(session.Ready)
			case r.pane.Legible && r.pane.Working:
				r.instance.SetActivity(session.ActivityWorking, r.pane.BackgroundShells)
				r.instance.SetStatus(session.Running)
			case r.pane.Legible && r.pane.BackgroundShells > 0:
				r.instance.SetActivity(session.ActivityShell, r.pane.BackgroundShells)
				r.instance.SetStatus(session.Running)
			case r.pane.Legible:
				// Only a prompt auto-yes is about to answer gets here: it is not a
				// finished turn, so it neither clears the activity nor stamps a
				// finish, and the next tick sees whatever answering it produced.
				if r.pane.HasPrompt {
					r.instance.TapEnter()
					break
				}
				r.instance.SetActivity(session.ActivityIdle, 0)
				r.instance.SetStatus(session.Ready)
			case r.pane.Updated:
				r.instance.SetStatus(session.Running)
			case r.pane.HasPrompt:
				r.instance.TapEnter()
			default:
				r.instance.SetStatus(session.Ready)
			}
			if event := attentionEvent(r.instance, before, wasDone); event != "" {
				events = append(events, attention{title: r.instance.Title, event: event})
			}
			// A skipped diff is not an empty one: leave the counts the row is
			// already showing in place.
			if r.diffComputed {
				if r.diffStats != nil && r.diffStats.Error != nil {
					if !strings.Contains(r.diffStats.Error.Error(), "base commit SHA not set") {
						log.WarningLog.Printf("could not update diff stats: %v", r.diffStats.Error)
					}
					r.instance.SetDiffStats(nil, false)
				} else {
					r.instance.SetDiffStats(r.diffStats, r.diffContent)
				}
				// ComputeDiff always stages untracked files; ComputeDiffNumstat
				// does it only when asked.
				if r.diffUntracked || r.diffContent {
					r.instance.MarkUntrackedStaged()
				}
			}
			r.instance.SetCIStatus(r.ciStatus)
			r.instance.SetUpstreamStatus(r.upstream)
			if r.branchResolved {
				r.instance.SetCurrentBranch(r.currentBranch)
			}
		}
		if goneAny {
			// Persisted so the row is still parked after a restart, and the menu
			// re-read so it offers resume rather than attach.
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				log.WarningLog.Printf("could not save a session that went away: %v", err)
			}
			m.syncSelection()
		}
		m.syncDevBadge()
		next := tickUpdateMetadataCmd(m.planMetadata(m.snapshotActiveInstances()),
			m.appConfig.CIStatusEnabled(), m.appConfig.UpstreamStatusEnabled(), m.devStack)
		// A session that has picked work back up needs the animation restarted;
		// its own tick chain ended when nothing was moving.
		if notify := m.notifyCmd(events); notify != nil {
			next = tea.Batch(next, notify)
		}
		if !m.spinnerRunning && m.spinnerNeeded() {
			m.spinnerRunning = true
			return m, tea.Batch(next, m.spinner.Tick)
		}
		return m, next
	case runUpdateMsg:
		return m, runUpdateCmd(msg)
	case updateDoneMsg:
		// No message on success: the badge clearing on the next tick is the
		// report, and it is the thing that was being complained about.
		if msg.err != nil {
			return m, tea.Batch(m.instanceChanged(), m.handleError(msg.err))
		}
		return m, m.instanceChanged()
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.Status == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.tabbedWindow.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.tabbedWindow.ScrollDown()
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		input := m.promptInput()
		if input == nil {
			return m, nil
		}
		if msg.version != input.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if input := m.promptInput(); input != nil {
			input.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		// Clear before the next frame. A resize is also what arrives when a tmux
		// client reattaches to a session Adroit has been running in unwatched --
		// which is the normal way back in after a terminal window is closed -- and
		// the renderer draws the next frame as a diff against the one it last
		// sent, to a screen that is no longer showing it. Anything that was on the
		// old frame and not the new one stays on screen otherwise, which reads as
		// the interface having come back corrupt.
		return m, tea.ClearScreen
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case noticeMsg:
		return m, m.handleNotice(string(msg))
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case killApprovedMsg:
		return m, m.killApproved(msg.instance)
	case killFinishedMsg:
		// The row is already gone, so this is not a failed kill from where the user
		// is sitting -- it is a leftover they need to know about.
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		return m, nil
	case instanceResumedMsg:
		delete(m.busy, msg.instance)
		m.list.ClearBusy(msg.instance)
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		// Say which kind of resume this was. A session whose tmux died -- a
		// reboot, or the WSL VM stopping with the last terminal window -- is
		// restarted rather than reattached, and a restarted agent that kept its
		// conversation is indistinguishable from one that lost it until you
		// scroll. Only worth saying when there was a conversation at stake: an
		// ordinary pause and resume never restarts anything.
		// The ordinary resume gets a line too: the help card that used to stand
		// in for one is shown once per install, and after that a key that takes
		// seconds should still say when it is done.
		notice := m.handleNotice(fmt.Sprintf("resumed %s", msg.instance.Title))
		if msg.instance.ResumedConversation() {
			notice = m.handleNotice(fmt.Sprintf("resumed %s with its conversation", msg.instance.Title))
		}
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), notice)
	case overlay.SettingsCheckMsg:
		if s, ok := m.overlay.(*settingsScreen); ok {
			s.settings.HandleCheck(msg)
		}
		return m, nil
	case instancePausedMsg:
		delete(m.busy, msg.instance)
		m.list.ClearBusy(msg.instance)
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		m.tabbedWindow.CleanupTerminalForInstance(msg.instance.Title)
		// Saved now, as start, resume and kill are. The pause has already
		// removed the worktree, so a state file still saying Running would,
		// after a crash, bring the row back as a running session in a directory
		// that no longer exists -- and the next pause or resume deletes whatever
		// the agent wrote there in the meantime.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		// What the checkout card said, in one line, every time: where the branch
		// went and which key brings the session back.
		notice := fmt.Sprintf("checked out %s · r resumes it", msg.instance.Title)
		if msg.instance.BranchCopied() {
			notice = fmt.Sprintf("checked out %s · branch on the clipboard · r resumes it", msg.instance.Title)
		}
		return m, tea.Batch(m.instanceChanged(), m.handleNotice(notice))
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			m.list.Kill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		if msg.promptAfterName {
			m.menu.SetState(ui.StatePrompt)
			m.overlay = &promptScreen{input: m.newPromptOverlay()}
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.ErrorLog.Printf("failed to send prompt: %v", err)
				}
				msg.instance.Prompt = ""
			}
			m.menu.SetState(ui.StateDefault)
			m.showHelpScreen(helpStart(msg.instance), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		if !m.spinnerNeeded() {
			m.spinnerRunning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
}
