package app

import (
	"context"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/truncate"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool) error {
	p := tea.NewProgram(
		newHome(ctx, program, autoYes),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateTheme is the state when the colour theme picker is open.
	stateTheme
	// stateRestore is the state when the killed-session picker is open.
	stateRestore
)

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

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// instanceStarting is true while a background instance start is in progress.
	// Prevents double-submission and guards against interacting with a not-yet-started instance.
	instanceStarting bool
	// startingInstance holds a reference to the instance being started in the background.
	startingInstance *session.Instance

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
	// termWidth is the terminal's own width, kept so the composed frame can be
	// held to it. Nothing may be drawn wider than the screen: tmux clips the
	// overflow, and what it clips is the right-hand edge of the interface.
	termWidth int
	// spinnerRunning is whether the animation's tick chain is alive. The chain
	// ends when nothing on screen is animating, so something has to know to start
	// it again -- see spinnerNeeded.
	spinnerRunning bool
	// previewHash fingerprints the capture the preview pane is currently showing,
	// so the tick can tell a pane that has not changed from one that has.
	previewHash uint64
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
	// pendingAction holds a confirmed action until the update loop can hand it to
	// Bubble Tea as a Cmd. The overlay's OnConfirm callback is invoked from inside
	// a key handler, so an action run there runs ON the update loop, for as long
	// as it takes -- and a kill tears down a tmux session, a git worktree and the
	// dev stack, none of which is bounded. Parking it here costs one frame and
	// keeps the interface answering while it happens.
	pendingAction tea.Cmd

	// devStack owns the singleton development stack. Nil is impossible; an
	// unconfigured stack is a live object that reports itself unconfigured, so
	// every call site can ask rather than nil-check.
	devStack *dev.Stack

	// themePicker is open only in stateTheme. themeBeforePicker is the palette
	// that was in use when it opened, kept here rather than in the picker so
	// cancelling restores it from the one place that installed it.
	themePicker       *overlay.ThemePicker
	themeBeforePicker theme.Palette

	// restorePicker is open only in stateRestore: the list of sessions that have
	// been killed and whose conversation can be picked back up.
	restorePicker *overlay.RestorePicker
}

// reapOrphanedTerminals kills the Terminal-tab shells of sessions that are gone.
//
// The pane closes a terminal when its session is killed in the same run, but a
// terminal outlives the process that made it, and nothing ever looked at the
// ones left behind. They accumulate for as long as the tmux server lives: seven
// were found on this machine, for sessions killed days earlier.
//
// Only the terminals. An agent's own session carries a live conversation and is
// deliberately left alone -- it is what pausing and resuming rely on, and a
// session missing from the list is not proof its work is finished.
func reapOrphanedTerminals(instances []*session.Instance) {
	reapOrphanedTerminalsWith(cmd.MakeExecutor(), instances)
}

func reapOrphanedTerminalsWith(exec cmd.Executor, instances []*session.Instance) {
	names, err := tmux.ListSessionNames(exec)
	if err != nil {
		log.WarningLog.Printf("could not list tmux sessions to reap terminals: %v", err)
		return
	}

	live := make(map[string]bool, len(instances))
	for _, instance := range instances {
		live[tmux.SessionNameFor(ui.TerminalSessionName(instance.Title))] = true
	}

	prefix := tmux.SessionNameFor(ui.TerminalSessionName(""))
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || live[name] {
			continue
		}
		if err := tmux.KillSession(exec, name); err != nil {
			log.WarningLog.Printf("could not reap orphaned terminal %s: %v", name, err)
			continue
		}
		log.InfoLog.Printf("reaped orphaned terminal session %s", name)
	}
}

// orphanedWorktrees lists the folders in the worktrees directory that no stored
// session's worktree lives in. A kill whose teardown timed out, or a crash
// part way through creating a session, leaves one behind, and nothing looked:
// two were found on this machine, one of them holding uncommitted work. They are
// reported rather than removed for exactly that reason.
func orphanedWorktrees(dir string, instances []*session.Instance) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var owned []string
	for _, instance := range instances {
		if p := instance.ToInstanceData().Worktree.WorktreePath; p != "" {
			owned = append(owned, filepath.Clean(p))
		}
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		top := filepath.Join(dir, entry.Name())
		isOwned := false
		for _, p := range owned {
			// A branch name with a slash nests the worktree a level down, so the
			// folder is owned if any session's worktree is it or lies inside it.
			if p == top || strings.HasPrefix(p, top+string(filepath.Separator)) {
				isOwned = true
				break
			}
		}
		if !isOwned {
			orphans = append(orphans, entry.Name())
		}
	}
	return orphans
}

// attachFinishedMsg is delivered once the user has detached from a session.
type attachFinishedMsg struct{ err error }

// attachCommand adapts an attach to bubbletea's ExecCommand, whose contract is
// "take the terminal, block until done, give it back".
//
// The stream setters are deliberately empty. An ExecCommand is normally an
// exec.Cmd that has to be told where its stdio goes; the attach talks to
// os.Stdin and os.Stdout directly, through the PTY the tmux client is on, and
// those are the same two files bubbletea would hand over here.
type attachCommand struct {
	attach func() (chan struct{}, error)
}

func (c *attachCommand) Run() error {
	ch, err := c.attach()
	if err != nil {
		return err
	}
	<-ch
	return nil
}

func (c *attachCommand) SetStdin(io.Reader)  {}
func (c *attachCommand) SetStdout(io.Writer) {}
func (c *attachCommand) SetStderr(io.Writer) {}

// attachCmd hands the terminal to a session for as long as the user is
// attached to it.
//
// tea.Exec rather than releasing the terminal by hand, and this is the whole
// reason: bubbletea dispatches an Exec from its event loop, not from Update.
// Attaching from inside Update cannot release the terminal cleanly, because
// bubbletea's input reader delivers keystrokes over an unbuffered channel and
// blocks on the send -- and while Update is running, nothing is receiving.
// ReleaseTerminal cancels the reader, but a cancel does not interrupt a
// channel send, so it gives up after 500ms and leaves the reader alive. The
// moment Update returns and the event loop drains that send, the reader loops
// round and reads stdin again, swallowing exactly one keystroke: the first
// thing typed into a freshly attached session, gone, every time the attach was
// reached through a help screen. From the event loop the send has already
// landed, the reader is idle in a read, and the cancel works.
func (m *home) attachCmd(attach func() (chan struct{}, error)) tea.Cmd {
	return tea.Exec(&attachCommand{attach: attach}, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
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
		errBox:    ui.NewErrBox(),
		storage:   storage,
		appConfig: appConfig,
		program:   program,
		autoYes:   autoYes,
		state:     stateDefault,
		appState:  appState,
		devStack:  devStack,
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
	// List takes 30% of width, preview takes 70%
	listWidth := int(float32(msg.Width) * 0.3)
	tabsWidth := msg.Width - listWidth

	// The menu is one row and the error box another; everything else is content.
	// The menu used to take 10% of the height -- four or five rows at 45, for one
	// line of text -- and those rows now go to the list and the preview.
	//
	// The rows must add up exactly: a frame composed one row taller than the
	// terminal scrolls the whole view by one on every redraw.
	const menuHeight = 1
	contentHeight := max(msg.Height-menuHeight-1, 0) // and the error box
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1)

	m.tabbedWindow.SetSize(tabsWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(int(float32(msg.Width)*0.6), int(float32(msg.Height)*0.4))
		m.textInputOverlay.SetMaxHeight(msg.Height)
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(msg.Width, menuHeight)
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
		m.previewTick(),
		tickUpdateMetadataCmd(m.planMetadata(m.snapshotActiveInstances()),
			m.appConfig.CIStatusEnabled(), m.appConfig.UpstreamStatusEnabled(), m.devStack),
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case attachFinishedMsg:
		// Back from a session. Both panes and the session itself may be the wrong
		// shape now: it was resized to the real terminal while attached, and the
		// terminal may have been resized while we were not watching.
		m.state = stateDefault
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
	case previewTickMsg:
		var cmd tea.Cmd
		if msg.hasContent {
			// The capture is in hand, so nothing here touches tmux: apply it and
			// sync the panes that read cached state.
			m.previewHash = msg.hash
			m.tabbedWindow.ApplyPreviewCapture(m.list.GetSelectedInstance(), msg.content)
			m.syncSelection()
		} else {
			cmd = m.instanceChanged()
		}
		return m, tea.Batch(cmd, m.previewTick())
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case instanceStartDoneMsg:
		m.instanceStarting = false
		inst := msg.instance
		m.startingInstance = nil

		if msg.err != nil {
			// Start failed — remove the instance from the list and show the error.
			m.list.Kill()
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), m.handleError(msg.err))
		}

		// Save after successful start.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}

		if m.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = overlay.NewTextInputOverlay("Enter prompt", "")
			m.promptAfterName = false
		} else {
			m.showHelpScreen(helpStart(inst), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
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
		if m.textInputOverlay == nil {
			return m, nil
		}
		if msg.version != m.textInputOverlay.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.textInputOverlay != nil {
			m.textInputOverlay.SetBranchResults(msg.branches, msg.version)
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
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		// Say which kind of resume this was. A session whose tmux died -- a
		// reboot, or the WSL VM stopping with the last terminal window -- is
		// restarted rather than reattached, and a restarted agent that kept its
		// conversation is indistinguishable from one that lost it until you
		// scroll. Only worth saying when there was a conversation at stake: an
		// ordinary pause and resume never restarts anything.
		var notice tea.Cmd
		if msg.instance.ResumedConversation() {
			notice = m.handleNotice(fmt.Sprintf("resumed %s with its conversation", msg.instance.Title))
		}
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), notice)
	case instancePausedMsg:
		delete(m.busy, msg.instance)
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		m.tabbedWindow.CleanupTerminalForInstance(msg.instance.Title)
		return m, m.instanceChanged()
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
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newPromptOverlay()
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

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	// The overlays own every key while they are open; highlighting a menu entry
	// underneath one, and re-sending the key to do it, is both invisible and a
	// second delivery of a keypress the overlay already handled.
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm ||
		m.state == stateTheme || m.state == stateRestore {
		return nil, false
	}
	// While a name is being typed the row owns every printable key. Most letters
	// are also shortcuts here -- n, r, t, d, x, p, c, g, o -- so without this,
	// typing "post-ai-svg" highlighted a different menu entry on nearly every
	// keystroke and re-sent each one through the update loop to get it delivered.
	// Enter, tab and escape still highlight: they are the ones the menu is
	// advertising while the row is being named.
	if m.state == stateNew {
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace:
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
	// TODO: cleanup: when you press enter on stateNew, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNew {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	if m.state == stateTheme {
		return m.handleThemeState(msg)
	}

	if m.state == stateRestore {
		return m.handleRestoreState(msg)
	}

	if m.state == stateNew {
		// The preview follows every keystroke, and is cleared by every way out.
		defer func() {
			m.syncNaming(m.state == stateNew)
		}()

		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Type {
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
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				m.textInputOverlay = m.newPromptOverlay()
				// Trigger initial branch search (no debounce, version 0)
				initialSearch := m.runBranchSearch("", m.textInputOverlay.BranchFilterVersion())
				return m, tea.Batch(tea.WindowSize(), initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.state = stateDefault
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

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
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
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
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
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newBranchOverlay()
			version := m.textInputOverlay.BranchFilterVersion()
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
			return m, tea.Batch(tea.WindowSize(), initialSearch, refreshed)
		case tea.KeyEsc:
			m.list.Kill()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Handle cancel via ctrl+c before delegating to the overlay
		if msg.String() == "ctrl+c" {
			return m, m.cancelPromptOverlay()
		}

		// Use the new TextInputOverlay component to handle all key events
		shouldClose, branchFilterChanged := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			if selected == nil {
				return m, nil
			}

			if m.textInputOverlay.IsCanceled() {
				// Reached by tab from the name prompt, esc steps back to it with
				// the name kept. It used to throw the whole new session away.
				if m.branchFromNaming && !selected.Started() {
					m.branchFromNaming = false
					m.textInputOverlay = nil
					m.state = stateNew
					m.menu.SetState(ui.StateNewInstance)
					m.syncNaming(true)
					return m, tea.WindowSize()
				}
				return m, m.cancelPromptOverlay()
			}
			m.branchFromNaming = false

			if m.textInputOverlay.IsSubmitted() {
				prompt := m.textInputOverlay.GetValue()
				selectedBranch := m.textInputOverlay.GetSelectedBranch()
				selectedProgram := m.textInputOverlay.GetSelectedProgram()
				noWorktree := m.textInputOverlay.IsNoWorktreeSelected()

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
						m.textInputOverlay.Submitted = false
						m.textInputOverlay.SetError(err.Error())
						m.textInputOverlay.FocusBranchPicker()
						return m, nil
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
					m.textInputOverlay = nil
					m.state = stateDefault
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

					return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
				}

				// Instance already running (the `i` key): send the prompt and go
				// straight back to the list. Off the loop, since sending pauses
				// between the text and the enter.
				m.textInputOverlay = nil
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				if strings.TrimSpace(prompt) == "" {
					return m, tea.WindowSize()
				}
				title := selected.Title
				return m, tea.Batch(tea.WindowSize(), func() tea.Msg {
					if err := selected.SendPrompt(prompt); err != nil {
						return err
					}
					return noticeMsg(fmt.Sprintf("sent to %s", title))
				})
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
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
			filter := m.textInputOverlay.BranchFilter()
			version := m.textInputOverlay.BranchFilterVersion()
			return m, m.scheduleBranchSearch(filter, version)
		}

		return m, nil
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			// Run whatever was confirmed, off the loop. Whatever it returns -- an
			// error to show, or a step that does touch the model -- comes back
			// through Update like any other message.
			if action := m.pendingAction; action != nil {
				m.pendingAction = nil
				return m, action
			}
			return m, nil
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
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
		m.state = statePrompt
		m.menu.SetState(ui.StatePrompt)
		m.textInputOverlay = overlay.NewTextInputOverlay(fmt.Sprintf("Send to %s", selected.Title), "")
		return m, tea.WindowSize()
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
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
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
			Program: m.program,
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.syncNaming(true)

		return m, nil
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
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
		message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
		if detail := killDetail(selected); detail != "" {
			message += "\n\n" + detail
		}
		return m, m.confirmAction(message, killAction)
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
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
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
	case keys.KeyTheme:
		m.themeBeforePicker = theme.Current()
		m.themePicker = overlay.NewThemePicker(m.appConfig.Theme)
		m.state = stateTheme
		return m, nil
	case keys.KeyOpenPR:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
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

// syncSelection points the panes and the menu at the selected session. Reads
// cached state only -- no captures, no git -- so it is safe to run on every
// preview tick, where the expensive half has already been done off the loop.
func (m *home) syncSelection() {
	// selected may be nil
	selected := m.list.GetSelectedInstance()
	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	m.menu.SetInstance(selected)
	m.menu.SetAttention(m.list.HasAttention())

	// The stack is per repository, so what `d` would do here depends on which
	// row the cursor is on. Pointed on every selection sync rather than only on
	// a move: the list also changes under the cursor as sessions are added,
	// killed and restored.
	repo := ""
	if selected != nil {
		repo = selected.RepoPath()
	}
	m.devStack.Point(repo)
	m.menu.SetDevEnabled(m.devStack.Configured())
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
// syncDevBadge pushes the stack's current verdict onto the row that owns it.
func (m *home) syncDevBadge() {
	status := m.devStack.Snapshot()
	// The menu advertises the stop key off this, so it has to be told even when
	// there is nothing to badge.
	m.menu.SetDevRunning(status.Running)

	title, state := status.Title, status.State()
	if title == "" {
		state = dev.LampDown
	}
	m.list.SetDevStack(title, state)
	// The tab carries the same verdict, which is what answers the question from
	// a tab other than Run: the list badge only marks the row that owns it.
	m.tabbedWindow.SetDevStack(title, state)
}

// stopDevStack tears the development stack down, wherever it is running.
//
// Deliberately not scoped to the selected session: there is one stack, and the
// row the cursor is on has no bearing on it. Nor is it confirmed -- unlike
// killing a session, stopping a stack destroys no work and costs one keypress
// to undo.
func (m *home) stopDevStack() tea.Cmd {
	if !m.devStack.Configured() || m.devStack.RunningFor() == "" {
		return nil
	}

	// Cleared before the stop rather than after: the record exists so the next
	// run can adopt a stack that is still up, and a crash midway through a
	// teardown must not leave it pointing at one that is on its way out.
	if err := m.appState.SetDevStackInstance(""); err != nil {
		log.WarningLog.Printf("could not clear the dev stack session: %v", err)
	}

	stack := m.devStack
	return func() tea.Msg {
		return devStackStoppedMsg{err: stack.Stop()}
	}
}

// devStackStoppedMsg reports the outcome of tearing the stack down.
type devStackStoppedMsg struct {
	err error
}

// devStackStartedMsg reports the outcome of pointing the stack at a session.
type devStackStartedMsg struct {
	title string
	err   error
}

// startDevStack points the development stack at the selected session.
//
// The work happens off the update loop and takes seconds to tens of seconds: it
// waits for the previous stack's ports to be released and for shared services to
// answer. Blocking the loop for that would freeze the interface over exactly the
// interval the user most wants to watch.
func (m *home) startDevStack() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Status == session.Loading {
		return nil
	}
	// Asked of the selected session's repository rather than of the config as a
	// whole: a stack defined for another repository is not one that can run here,
	// and saying "no dev stack is configured" to someone who has three would be a
	// lie.
	repoPath := selected.RepoPath()
	if cfg := m.appConfig.DevFor(repoPath); cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return m.handleError(fmt.Errorf(
			"no dev stack is configured for %s: add an entry under \"dev_by_repo\" in the Adroit config",
			filepath.Base(filepath.Clean(repoPath))))
	}
	if selected.Status == session.Paused {
		return m.handleError(fmt.Errorf("session %s is paused: resume it before running its stack", selected.Title))
	}

	dir := selected.DevDir()
	if dir == "" {
		return m.handleError(fmt.Errorf("session %s has no directory to run a stack in", selected.Title))
	}

	title := selected.Title

	// Recorded before the start rather than after: the tmux session outlives this
	// process, so a crash between launching it and writing the record would leave
	// a stack running that the next run cannot attribute to anything.
	if err := m.appState.SetDevStackInstance(title); err != nil {
		log.WarningLog.Printf("could not record the dev stack session: %v", err)
	}
	m.list.SetDevStack(title, dev.LampPending)
	m.menu.SetDevRunning(true)

	// Switch to the tab that shows what is happening. The whole point of the key
	// is watching the stack come up, and it takes long enough that landing on the
	// agent's output instead reads as nothing having happened.
	m.tabbedWindow.SelectRunTab()

	stack := m.devStack
	return func() tea.Msg {
		return devStackStartedMsg{title: title, err: stack.Start(title, dir, repoPath)}
	}
}

// syncNaming tells the list a session is being named, and what branch the name
// typed so far would create.
//
// Computed here rather than in the renderer because it needs the loaded config
// -- the prefix and the case policy -- and the renderer runs every frame.
func (m *home) syncNaming(on bool) {
	if !on {
		m.list.SetNaming(false, "")
		return
	}
	title := ""
	if n := m.list.NumInstances(); n > 0 {
		title = m.list.GetInstances()[n-1].Title
	}
	m.list.SetNaming(true, git.PreviewBranchName(
		m.appConfig.BranchPrefixFor(m.namingRepo), m.appConfig.PreserveBranchCaseFor(m.namingRepo), title))
}

func (m *home) instanceChanged() tea.Cmd {
	m.syncSelection()
	selected := m.list.GetSelectedInstance()

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateRun(selected); err != nil {
		return m.handleError(err)
	}
	return nil
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

// hideErrMsg implements tea.Msg and clears the error text from the screen, if
// it is still the message numbered seq.
type hideErrMsg struct{ seq int }

// messageLifetime is how long a message stays: long enough to read. A flat 3s
// was too short for the teardown errors, which run to a sentence or two.
func messageLifetime(text string) time.Duration {
	d := 3*time.Second + time.Duration(len(text))*40*time.Millisecond
	return min(d, 10*time.Second)
}

// previewTickMsg drives the preview pane, carrying the capture the tick already
// took when it took one.
//
// The tick used to fire unconditionally every 100ms and have Update do the
// capture, which put a subprocess in the event loop ten times a second and
// re-rendered the whole interface whether or not a byte of the pane had changed.
// Now the capture happens in the command, and a pane that has not changed
// produces no message at all -- and so no render.
type previewTickMsg struct {
	content    string
	hasContent bool
	hash       uint64
}

const (
	// previewInterval is how often the selected session's pane is sampled.
	previewInterval = 100 * time.Millisecond
	// previewFloor is how long the tick will go without emitting anything at all.
	// A pane can be still while the state around it is not -- a session going
	// paused, an instance being named -- and this is the ceiling on how long the
	// pane can lag those.
	previewFloor = time.Second
)

// killApprovedMsg carries a session that has passed the checks a kill can fail,
// back onto the update loop so the model can be changed before anything is torn
// down.
type killApprovedMsg struct {
	instance *session.Instance
}

// killFinishedMsg reports a teardown that ran off the loop. The session is gone
// from the list either way by the time this arrives; what it can still carry is
// a worktree or a tmux session that would not go quietly.
type killFinishedMsg struct {
	title string
	err   error
}

type instanceChangedMsg struct{}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		currentDir, _ := os.Getwd()
		branches, err := git.SearchBranches(currentDir, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// previewTickCmd samples the selected session's pane until it differs from what
// the pane is already showing, then hands the capture back.
//
// capture is false wherever there is nothing to sample -- another tab is open,
// the pane is in scroll mode, the session is paused or not started -- and there
// the command degrades to a plain 100ms tick, which is what it always was.
func previewTickCmd(selected *session.Instance, capture bool, lastHash uint64) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(previewFloor)
		for {
			time.Sleep(previewInterval)
			if !capture {
				return previewTickMsg{}
			}
			content, err := selected.Preview()
			if err != nil {
				// Let Update take the ordinary path, which reports it in the pane.
				return previewTickMsg{}
			}
			if h := hashPreview(content); h != lastHash || time.Now().After(deadline) {
				return previewTickMsg{content: content, hasContent: true, hash: h}
			}
		}
	}
}

// hashPreview fingerprints a capture. FNV-1a because the only question asked of
// it is "same as last time", over a few tens of kilobytes, a few times a second.
func hashPreview(content string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return h.Sum64()
}

// previewTick builds the next preview tick from the current state of the
// interface.
func (m *home) previewTick() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	capture := selected != nil &&
		selected.Started() && selected.Status != session.Paused &&
		m.tabbedWindow.IsInPreviewTab() && !m.tabbedWindow.IsPreviewInScrollMode()
	return previewTickCmd(selected, capture, m.previewHash)
}

// spinnerNeeded reports whether anything on screen is animating.
//
// The spinner used to re-tick forever, so an interface with nothing moving in it
// still re-rendered twelve times a second for the life of the process. Stopping
// the chain when nothing spins means an idle list renders only when something
// actually changes; the metadata tick re-evaluates this twice a second, so the
// animation restarts within a tick of a session picking work back up.
func (m *home) spinnerNeeded() bool {
	if m.instanceStarting {
		return true
	}
	for _, inst := range m.list.GetInstances() {
		if inst.Status == session.Loading {
			return true
		}
		switch inst.GetActivity() {
		case session.ActivityWorking, session.ActivityShell:
			return true
		}
	}
	return false
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance *session.Instance
	// pane is one capture's worth of everything the session's pane says.
	pane tmux.PaneState
	// diffStats is nil both when there was nothing to compute and when the tick
	// deliberately skipped the computation, which diffComputed distinguishes: a
	// skipped diff must leave the last good counts on the row rather than blank
	// them.
	diffStats    *git.DiffStats
	diffComputed bool
	diffContent  bool
	// diffUntracked records that this diff folded untracked files in, which the
	// instance stamps so the walk is not paid for again immediately.
	diffUntracked bool
	ciStatus      ci.Status
	upstream      upstream.Status
	// currentBranch is "" when the worktree's HEAD could not be resolved, which
	// the instance reads as "keep the recorded name". Only meaningful when
	// branchResolved is set, for the same reason as diffComputed.
	currentBranch  string
	branchResolved bool
}

// metadataPlan is what one instance's slot in a tick is allowed to do.
//
// Decided on the main thread, where the instance's timestamps can be read
// without racing the renderer, and handed to the goroutine that does the work.
type metadataPlan struct {
	instance *session.Instance
	// diffDue is true when the diff is old enough to recompute regardless of
	// whether the pane changed.
	diffDue bool
	// diffAllowed is true when enough time has passed to honour a changed pane.
	diffAllowed bool
	// stageUntracked is true when this diff should also pay for the untracked
	// walk.
	stageUntracked bool
	// wantContent is true when the Diff tab is showing this session, which is the
	// only time the full diff text is worth loading.
	wantContent bool
	// branchDue is true when the checked-out branch is worth re-reading.
	branchDue bool
}

const (
	// diffMaxAgeSelected and diffMaxAgeOther are the floors under the
	// pane-changed gate: how long a diff can go unrecomputed when nothing has
	// been drawn to the session's pane at all. Only a background shell writing
	// with nothing on screen to show for it gets this far.
	diffMaxAgeSelected = 4 * time.Second
	diffMaxAgeOther    = 10 * time.Second
	// diffCooldownSelected and diffCooldownOther are the ceilings over the same
	// gate: the fastest a changing pane can ask for a new diff.
	//
	// Necessary because "the pane changed" is a much weaker signal than it looks:
	// an agent mid-turn redraws continuously -- its own spinner is enough -- so
	// the gate is open on every tick for exactly the sessions that are busy, and
	// without a ceiling those sessions pay the full twice-a-second cost the gate
	// was meant to avoid. Measured on a live list: the gate alone left ~72ms/sec
	// on the table.
	//
	// The selected row is the one being read; the rest are decoration on a line
	// that is mostly branch name.
	diffCooldownSelected = time.Second
	diffCooldownOther    = 3 * time.Second
	// untrackedMaxAge is how often a diff pays for the `add -N` walk that folds
	// untracked files into the counts -- 12.1ms measured, more than the diff
	// itself. A file appearing untracked is rare next to the rate of this tick.
	untrackedMaxAge = 15 * time.Second
	// branchMaxAge is how long a resolved branch name is trusted. Renaming or
	// switching a branch inside a worktree is legitimate but rare, and the name
	// is only used for display.
	branchMaxAge = 30 * time.Second
)

// runUpdateMsg asks the update loop to start bringing a worktree up to date.
//
// The confirmation overlay runs its action on the main thread, so the action
// itself cannot do the work: a fetch and a merge are seconds of network and disk
// during which the interface would be frozen. It returns this instead, and the
// loop turns it into a background command.
type runUpdateMsg struct {
	instance *session.Instance
	// reset discards the worktree's own commits and takes the remote's history
	// wholesale, which is the only way past a divergence.
	reset bool
}

// updateDoneMsg is sent when a worktree has been brought up to date, or failed
// to be.
type updateDoneMsg struct {
	err error
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// instanceStartDoneMsg is sent when the background instance start completes.
type instanceStartDoneMsg struct {
	instance *session.Instance
	err      error
}

// instanceResumedMsg and instancePausedMsg carry the result of a resume or a
// checkout's pause back from offLoop.
type instanceResumedMsg struct {
	instance *session.Instance
	err      error
}

type instancePausedMsg struct {
	instance *session.Instance
	err      error
}

// offLoop runs work that shells out to git and tmux as a Cmd, off the update
// loop, marking the session busy until its message comes back. A session that
// is already busy is left alone: the press is answered, not queued.
func (m *home) offLoop(instance *session.Instance, doing string, work func() tea.Msg) tea.Cmd {
	if m.busy[instance] {
		return m.handleNotice(fmt.Sprintf("still %s %s", doing, instance.Title))
	}
	if m.busy == nil {
		m.busy = map[*session.Instance]bool{}
	}
	m.busy[instance] = true
	return tea.Batch(m.handleNotice(fmt.Sprintf("%s %s…", doing, instance.Title)), work)
}

// runInstanceStartCmd returns a Cmd that performs the expensive instance.Start(true)
// in a background goroutine so the main event loop stays responsive.
func runInstanceStartCmd(instance *session.Instance) tea.Cmd {
	return func() tea.Msg {
		err := instance.Start(true)
		return instanceStartDoneMsg{instance: instance, err: err}
	}
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// planMetadata decides, on the main thread, what the next tick is allowed to
// spend on each session.
func (m *home) planMetadata(active []*session.Instance) []metadataPlan {
	selected := m.list.GetSelectedInstance()
	// The full diff text is only ever rendered by the diff pane. Computed for the
	// selected session on every tick regardless of which tab was open, it was a
	// whole diff loaded into memory twice a second to be thrown away.
	diffTabOpen := m.tabbedWindow.IsInDiffTab()

	plans := make([]metadataPlan, 0, len(active))
	for _, inst := range active {
		maxAge, cooldown := diffMaxAgeOther, diffCooldownOther
		wantContent := false
		if inst == selected {
			maxAge, cooldown = diffMaxAgeSelected, diffCooldownSelected
			wantContent = diffTabOpen
		}
		plans = append(plans, metadataPlan{
			instance:       inst,
			diffDue:        inst.DiffStale(maxAge, wantContent),
			diffAllowed:    inst.DiffStale(cooldown, wantContent),
			wantContent:    wantContent,
			stageUntracked: inst.UntrackedStale(untrackedMaxAge),
			branchDue:      inst.BranchStale(branchMaxAge),
		})
	}
	return plans
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(plans []metadataPlan, ciEnabled, upstreamEnabled bool, stack *dev.Stack) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		// Detached, not waited on. Every readiness probe is a network operation
		// with a timeout, and on this goroutine a hung one held up the diff and
		// status of every session in the list; the stack writes its own state and
		// the interface reads it on the next render. Poll paces itself and
		// refuses to overlap, so calling it every tick costs nothing once the
		// lamps are green.
		go stack.Poll()

		if len(plans) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(plans))
		var wg sync.WaitGroup
		for idx, plan := range plans {
			wg.Add(1)
			go func(i int, plan metadataPlan) {
				defer wg.Done()
				instance := plan.instance
				r := &results[i]
				r.instance = instance
				r.pane = instance.PollPane()

				// The capture above is the cheap half of this tick, and it answers
				// the expensive half's question: a session whose pane has not
				// changed has almost certainly not written to disk either, so the
				// diff it produced last time still stands. plan.diffDue is the
				// floor under that inference.
				if plan.diffDue || (r.pane.Updated && plan.diffAllowed) {
					r.diffComputed = true
					r.diffContent = plan.wantContent
					r.diffUntracked = plan.stageUntracked
					if plan.wantContent {
						r.diffStats = instance.ComputeDiff()
					} else {
						r.diffStats = instance.ComputeDiffNumstat(plan.stageUntracked)
					}
				}
				if ciEnabled {
					r.ciStatus = ciStatusFor(instance)
				}
				if upstreamEnabled {
					r.upstream = upstreamStatusFor(instance)
				}
				if plan.branchDue {
					r.branchResolved = true
					r.currentBranch = instance.ResolveCurrentBranch()
				}
			}(idx, plan)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return m.hideMessageAfter(messageLifetime(err.Error()))
}

// hideMessageAfter numbers the message just shown and clears it after d, unless
// another has replaced it by then.
func (m *home) hideMessageAfter(d time.Duration) tea.Cmd {
	m.errSeq++
	seq := m.errSeq
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(d):
		}
		return hideErrMsg{seq: seq}
	}
}

// noticeMsg carries a notice back from work done off the loop.
type noticeMsg string

// handleNotice reports something that went right, on the same row and the same
// timer as an error.
func (m *home) handleNotice(notice string) tea.Cmd {
	log.InfoLog.Printf("%s", notice)
	m.errBox.SetNotice(notice)
	return m.hideMessageAfter(messageLifetime(notice))
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

// titleForRepo names a session that runs in the repository itself, after the
// directory it runs in.
//
// Unlike a branch, a repeat is legitimate — several scratch terminals on one
// checkout is the point — so a clash takes a suffix rather than being refused.
// Which is why this cannot fail: there is always a directory, and demanding a
// typed name for a session started by pressing tab and enter was a rule nothing
// on screen had announced. Reporting it through the error box made it worse
// still: under the overlay, that reads as the key having done nothing.
func titleForRepo(path string, existing []*session.Instance) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "repo"
	}

	taken := make(map[string]bool, len(existing))
	for _, inst := range existing {
		taken[inst.Title] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
}

// titleCollision refuses a name whose tmux session another session already has.
//
// Compared by tmux name, not by title: tmux drops whitespace and turns "." and
// ":" into "_", so "fix login" and "fixlogin" are one tmux session. Two rows on
// one session attach to the same agent, and killing either kills both.
func titleCollision(inst *session.Instance, existing []*session.Instance) error {
	want := tmux.SessionNameFor(inst.Title)
	for _, other := range existing {
		if other == inst || other.Title == "" {
			continue
		}
		if tmux.SessionNameFor(other.Title) == want {
			if other.Title == inst.Title {
				return fmt.Errorf("a session named %q already exists", inst.Title)
			}
			return fmt.Errorf("%q is too close to the existing session %q: both become tmux session %s",
				inst.Title, other.Title, want)
		}
	}
	return nil
}

// titleForBranch names a session after the branch it is opening.
//
// The branch name is used verbatim: the 32-character cap on the name prompt is a
// typing convenience, not a constraint on the value -- the list truncates a long
// title for display already -- and a title that is exactly the branch is what
// makes the duplicate check below mean "you already have a session on this
// branch" rather than "you happened to pick the same words".
func titleForBranch(branch string, existing []*session.Instance) (string, error) {
	if branch == "" {
		return "", fmt.Errorf("type a name for the session, or pick an existing branch to take its name from")
	}
	// Title is the storage key and the tmux session name, so a collision would
	// otherwise surface much later as "tmux session already exists".
	for _, inst := range existing {
		if tmux.SessionNameFor(inst.Title) == tmux.SessionNameFor(branch) {
			return "", fmt.Errorf("a session for branch %q already exists", branch)
		}
	}
	return branch, nil
}

// killDetail says what a kill of this session throws away, from what the row
// already knows -- nothing is computed here, on the loop. The bare "Kill
// session?" said none of it, though the kill deletes the worktree with whatever
// is uncommitted in it and, for a branch Adroit created, the branch too.
func killDetail(inst *session.Instance) string {
	if inst.NoWorktree() {
		return "It runs in the repository itself, so only the session goes; your checkout is left alone."
	}
	switch inst.GetCIStatus().State {
	case ci.StateMerged:
		return fmt.Sprintf("PR #%d is merged, so this is safe to remove.", inst.GetCIStatus().PRNumber)
	case ci.StateClosed:
		return fmt.Sprintf("PR #%d was closed without merging.", inst.GetCIStatus().PRNumber)
	}
	var parts []string
	if st := inst.GetDiffStats(); st != nil && st.Error == nil && !st.IsEmpty() {
		parts = append(parts, fmt.Sprintf("+%d −%d against its base", st.Added, st.Removed))
	}
	switch up := inst.GetUpstreamStatus(); {
	case up.Tracked() && up.Ahead > 0:
		parts = append(parts, fmt.Sprintf("%d commit(s) not pushed", up.Ahead))
	case !up.Tracked() && !up.FetchedAt.IsZero():
		parts = append(parts, "the branch was never pushed")
	}
	if len(parts) == 0 {
		return "The worktree will be removed; R brings the conversation back."
	}
	return strings.Join(parts, ", ") + ". The worktree goes with it; R brings back the conversation, not the code."
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

// updateFromRemote brings the selected session's worktree up to date with the
// branch as it stands on the remote, behind a confirmation.
//
// A worktree is created once and never moves again on its own, so a session
// opened to review a branch someone is still pushing to reads an older and older
// copy of it. This is the way out, and the badge on the row is what asks for it.
//
// The confirmation is not ceremony: a fast-forward lands somebody else's commits
// in a tree an agent may be working in, and the diverged case discards commits
// outright. Both are stated in the prompt, counted, before anything happens.
func (m *home) updateFromRemote() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Status == session.Loading {
		return nil
	}
	if !m.appConfig.UpstreamStatusEnabled() {
		return m.handleError(fmt.Errorf(`upstream tracking is off — remove "upstream_status": false from the config to use u`))
	}
	// The user's own checkout, shared with every other session on the repo and
	// possibly holding uncommitted work. Moving it from a list of sessions would
	// be action at a distance.
	if selected.NoWorktree() {
		return m.handleError(fmt.Errorf("%q runs in the repo itself — pull it yourself", selected.Title))
	}

	status := selected.GetUpstreamStatus()
	switch {
	case !status.Tracked():
		return m.handleError(fmt.Errorf("%q has no branch on the remote yet — push it first with p", selected.Title))
	case status.Diverged():
		// A force-push upstream, in the ordinary case: no merge can reconcile the
		// two histories, so the only way to the remote's version is to abandon
		// ours. Named in full, since it is the one action here that loses work.
		message := fmt.Sprintf("[!] %s diverged: %d commit(s) here are not on it.\nReset '%s' to the remote and discard them?",
			status.Ref, status.Ahead, selected.Title)
		return m.confirmAction(message, func() tea.Msg {
			return runUpdateMsg{instance: selected, reset: true}
		})
	case !status.Stale():
		return m.handleError(fmt.Errorf("%q is already up to date with %s", selected.Title, status.Ref))
	default:
		message := fmt.Sprintf("[!] Pull %d new commit(s) from %s into '%s'?",
			status.Behind, status.Ref, selected.Title)
		return m.confirmAction(message, func() tea.Msg {
			return runUpdateMsg{instance: selected}
		})
	}
}

// runUpdateCmd performs the fetch and the fast-forward (or reset) off the event
// loop.
//
// It re-reads the counts from the freshly fetched refs rather than trusting the
// badge that prompted it: the badge is up to a minute old, and a merge decided
// from a stale count is exactly the mistake this feature exists to prevent.
func runUpdateCmd(msg runUpdateMsg) tea.Cmd {
	return func() tea.Msg {
		worktree, err := msg.instance.GetGitWorktree()
		if err != nil {
			return updateDoneMsg{err: err}
		}
		repoPath, worktreePath := worktree.GetRepoPath(), worktree.GetWorktreePath()
		if msg.reset {
			_, err = upstream.Reset(repoPath, worktreePath, msg.instance.Branch)
		} else {
			_, err = upstream.Pull(repoPath, worktreePath, msg.instance.Branch)
		}
		if err != nil {
			return updateDoneMsg{err: fmt.Errorf("could not update %q: %w", msg.instance.Title, err)}
		}
		return updateDoneMsg{}
	}
}

// killApproved performs the half of a kill that has to happen on the update
// loop, and returns the half that must not.
//
// The split is between what changes the model and what waits on another
// process. Recording the session for restore, dropping its terminal pane, its
// storage row and its row in the list are all model writes, and a Cmd's
// goroutine may not make them. Stopping the dev stack and Instance.Kill are the
// opposite: the first waits out SIGTERM and then the ports, the second shells
// out to tmux kill-session and git worktree remove. Those three ran here until
// now, and a kill held every keystroke and every frame for as long as they
// took -- indefinitely, when a git lock was held elsewhere.
//
// Taking the row out first is also what makes the keypress feel answered: the
// session disappears immediately, and its remains are cleared up behind it.
func (m *home) killApproved(instance *session.Instance) tea.Cmd {
	// Removed before anything else, so the list is right even if a later step
	// fails. Remove hands back the instance it dropped rather than leaving us to
	// look it up again from a selection that has already moved on.
	removed := m.list.Remove()
	if removed == nil {
		return nil
	}
	if removed.Title != instance.Title {
		// The selection cannot move while the confirmation is up, so this should be
		// unreachable; tear down what was actually removed if it ever is not.
		log.WarningLog.Printf("kill confirmed for %q but %q was selected; removing %q",
			instance.Title, removed.Title, removed.Title)
	}
	title := removed.Title

	// Before anything is torn down: what makes this session restorable is its
	// Claude transcript, and the worktree path that finds it is about to stop
	// existing.
	m.recordKilledSession(removed)

	// Clean up terminal session for this instance
	m.tabbedWindow.CleanupTerminalForInstance(title)

	if err := m.storage.DeleteInstance(title); err != nil {
		return m.handleError(err)
	}

	// A stack outlives the app but must not outlive the session it was running
	// for: its worktree is about to be removed out from under it, and it would go
	// on holding the ports with nothing left to serve. The badge and the stored
	// name are settled here; the stop itself goes below, with the other waiting.
	stopStack := m.devStack.RunningFor() == title
	if stopStack {
		if err := m.appState.SetDevStackInstance(""); err != nil {
			log.WarningLog.Printf("could not clear the dev stack session: %v", err)
		}
		m.syncDevBadge()
	}

	return tea.Batch(
		m.instanceChanged(),
		func() tea.Msg {
			// Ordered: the stack has to let go of the worktree before git can remove
			// it.
			if stopStack {
				if err := m.devStack.Stop(); err != nil {
					log.WarningLog.Printf("could not stop the dev stack: %v", err)
				}
			}

			if err := removed.Kill(); err != nil {
				return killFinishedMsg{
					title: title,
					err:   fmt.Errorf("%q is gone from the list but did not tear down cleanly: %w", title, err),
				}
			}
			return killFinishedMsg{title: title}
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

func (m *home) View() string {
	// Each pane leads with one blank row of its own, which is all the air the top
	// of the screen needs; a padding row here used to add a second.
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, m.list.String(), m.tabbedWindow.String())

	// Left, not centred: the menu spans the terminal and the panes stop just
	// short of it, so centring shifted the whole frame right by the difference
	// and left an unused strip on each side.
	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)
	mainView = m.fitToTerminal(mainView)

	// Overlays are fitted too, and after being placed: an overlay is sized off
	// the terminal, but the confirmation is a fixed 50 columns and a narrow
	// terminal is exactly where it does not fit.
	if m.state == statePrompt {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true))
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true))
	} else if m.state == stateTheme {
		if m.themePicker == nil {
			log.ErrorLog.Printf("theme picker is nil")
			return mainView
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.themePicker.Render(), mainView, true, true))
	} else if m.state == stateRestore {
		if m.restorePicker == nil {
			log.ErrorLog.Printf("restore picker is nil")
			return mainView
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.restorePicker.Render(), mainView, true, true))
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return m.fitToTerminal(overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true))
	}

	return mainView
}

// fitToTerminal holds the composed frame to the width of the screen.
//
// The panes budget themselves and the list and window each keep to the column
// they were given, so this changes nothing at a usable size. It is the backstop
// for the sizes where the budget cannot be met at all -- four bordered tabs need
// eight columns before a single label, so below about twenty the frame cannot be
// drawn to fit. Cut, the interface loses its right-hand edge; uncut, tmux cuts it
// anyway and the rows that overran take the layout with them.
func (m *home) fitToTerminal(view string) string {
	if m.termWidth <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > m.termWidth {
			lines[i] = truncate.String(line, uint(m.termWidth))
		}
	}
	return strings.Join(lines, "\n")
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

// ciStatusFor returns the cached CI verdict for an instance's branch. The lookup
// is non-blocking by contract (see ci.Get) because it runs inside the same
// metadata tick as the git diffs, which only re-schedules once every goroutine
// has finished.
func ciStatusFor(instance *session.Instance) ci.Status {
	// A session with no branch of its own has no pull request to report, and
	// resolving the repository's current HEAD would attribute somebody else's
	// branch to it.
	if instance.NoWorktree() {
		return ci.Status{}
	}
	worktree, err := instance.GetGitWorktree()
	if err != nil {
		return ci.Status{}
	}
	// The recorded branch is only the fallback: ci resolves the worktree's actual
	// HEAD, so a session whose branch was renamed or switched still finds its PR.
	return ci.Get(worktree.GetRepoPath(), worktree.GetWorktreePath(), instance.Branch)
}

// upstreamStatusFor returns the cached standing of an instance's branch against
// its remote counterpart. Non-blocking by the same contract as ciStatusFor, and
// for the same reason: it shares the tick that computes the diffs.
func upstreamStatusFor(instance *session.Instance) upstream.Status {
	// A session running in the repository itself is measured too, against
	// whatever its checkout has on HEAD. The row is not offered the key that
	// moves it -- that checkout is the user's, shared with every other session on
	// the repo -- but "your master is ten commits behind" is worth knowing, and
	// this is the only place it would be said.
	if instance.NoWorktree() {
		return upstream.Get(instance.Path, instance.Path, "")
	}
	worktree, err := instance.GetGitWorktree()
	if err != nil {
		return upstream.Status{}
	}
	return upstream.Get(worktree.GetRepoPath(), worktree.GetWorktreePath(), instance.Branch)
}
