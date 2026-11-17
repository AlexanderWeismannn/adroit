package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/theme"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

var terminalPaneStyle, terminalFooterStyle lipgloss.Style

func init() { theme.OnChange(applyTerminalTheme) }

func applyTerminalTheme() {
	terminalPaneStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	terminalFooterStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
}

// TerminalSessionName is the session name the Terminal tab runs a shell in.
//
// One definition, because a second one that drifted would leave the reaper at
// startup unable to recognise the sessions this pane creates -- and they outlive
// the process, so they would accumulate silently and forever.
func TerminalSessionName(instanceTitle string) string {
	return "term_" + instanceTitle
}

// terminalSession holds a cached tmux session for a specific instance.
type terminalSession struct {
	tmuxSession  *tmux.TmuxSession
	worktreePath string
}

// TerminalPane manages shell tmux sessions in the worktree directory of selected instances.
// Sessions are cached per instance so switching between instances preserves terminal state.
type TerminalPane struct {
	mu            sync.Mutex
	width, height int
	sessions      map[string]*terminalSession // instanceTitle → session
	// opening holds the shells being started off the loop, by instance title.
	// The token is how a start learns it was cancelled meanwhile -- a close for
	// that instance drops it -- so it closes what it made rather than storing it.
	opening      map[string]*struct{}
	currentTitle string // currently displayed instance
	content      string
	fallback     bool
	fallbackText string

	isScrolling bool
	viewport    viewport.Model
}

func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		sessions: make(map[string]*terminalSession),
		opening:  make(map[string]*struct{}),
		viewport: viewport.New(),
	}
}

func (t *TerminalPane) SetSize(width, height int) {
	width, height = max(width, 0), max(height, 0)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.width = width
	t.height = height
	t.viewport.SetWidth(width)
	t.viewport.SetHeight(height)
	if s, ok := t.sessions[t.currentTitle]; ok && s.tmuxSession != nil {
		if err := s.tmuxSession.SetDetachedSize(width, height); err != nil {
			log.InfoLog.Printf("terminal pane: failed to set detached size: %v", err)
		}
	}
}

// setFallbackState sets the terminal pane to display a fallback message.
// Caller must hold t.mu.
func (t *TerminalPane) setFallbackState(message string) {
	t.fallback = true
	t.fallbackText = splash(t.width, message)
	t.content = ""
}

// UpdateContent points the pane at instance and shows what its shell holds.
//
// It runs on the update loop, on every move of the cursor, so it does as little
// tmux as it can: one capture of a shell that is already open. Opening one --
// a has-session, a new-session, and a wait of up to two seconds for it to come
// up -- happens off the loop (see open), and the pane tick's Sample picks the
// shell up once it is there.
func (t *TerminalPane) UpdateContent(instance *session.Instance) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if instance == nil {
		t.setFallbackState("Select an instance to open a terminal")
		return nil
	}
	if instance.Status == session.Paused {
		t.setFallbackState("Session is paused. Resume to use terminal.")
		return nil
	}
	if !instance.Started() {
		t.setFallbackState("Instance is not started yet.")
		return nil
	}

	// Skip content updates while in scroll mode
	if t.isScrolling {
		return nil
	}

	if instance.GetWorktreePath() == "" {
		t.setFallbackState("Terminal session not available.")
		return nil
	}
	t.currentTitle = instance.Title

	s, ok := t.sessions[instance.Title]
	if !ok || s.tmuxSession == nil {
		t.openLocked(instance)
		return nil
	}

	content, err := s.tmuxSession.CapturePaneContent()
	if err != nil {
		// The shell's session has gone (it was exited, or tmux was). Dropping the
		// entry is not enough: its tmux client went with it, and the PTY we were
		// holding is what has to be closed for the process to be waited for
		// rather than left defunct. Not Close -- there is no session left to kill.
		log.InfoLog.Printf("terminal pane: %s's shell is gone, opening another: %v", instance.Title, err)
		if err := s.tmuxSession.CloseClient(); err != nil {
			log.InfoLog.Printf("terminal pane: %v", err)
		}
		delete(t.sessions, instance.Title)
		t.openLocked(instance)
		return nil
	}

	t.fallback = false
	t.content = content
	return nil
}

// Sample captures the shell pane the tab is showing for instance, off the
// event loop. It reports ok=false when there is nothing to sample -- no shell
// open for this instance yet, or the pane in scroll mode -- and never creates
// one: UpdateContent does that, on the loop, when the tab is opened.
//
// The capture forks tmux, so the lock is held only to find the session and
// again to apply the result; String() takes the same lock on every render and
// must not wait on a capture.
func (t *TerminalPane) Sample(instance *session.Instance) (content string, ok bool, err error) {
	t.mu.Lock()
	if instance == nil || t.isScrolling || instance.Title != t.currentTitle {
		t.mu.Unlock()
		return "", false, nil
	}
	s, found := t.sessions[t.currentTitle]
	t.mu.Unlock()
	if !found || s.tmuxSession == nil {
		return "", false, nil
	}
	content, err = s.tmuxSession.CapturePaneContent()
	if err != nil {
		return "", false, fmt.Errorf("terminal pane: failed to capture content: %w", err)
	}
	return content, true, nil
}

// ApplySample hands the pane a capture Sample took, unless the pane has moved
// on since: another instance, or scroll mode entered meanwhile.
func (t *TerminalPane) ApplySample(instance *session.Instance, content string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if instance == nil || t.isScrolling || instance.Title != t.currentTitle {
		return
	}
	t.fallback = false
	t.content = content
}

// openLocked starts a shell for instance off the loop, unless one is already on
// its way. The pane says so meanwhile. Caller must hold t.mu.
func (t *TerminalPane) openLocked(instance *session.Instance) {
	title, worktreePath := instance.Title, instance.GetWorktreePath()
	t.setFallbackState("Opening a terminal…")
	if t.opening[title] != nil {
		return
	}
	token := &struct{}{}
	t.opening[title] = token
	width, height := t.width, t.height

	go func() {
		ts, err := openShell(title, worktreePath, width, height)

		t.mu.Lock()
		defer t.mu.Unlock()
		if t.opening[title] != token {
			// Closed while it was opening: the instance was killed or checked out.
			if ts != nil {
				go closeShell(title, ts)
			}
			return
		}
		delete(t.opening, title)
		if err != nil {
			log.ErrorLog.Printf("%v", err)
			if t.currentTitle == title {
				t.setFallbackState(err.Error())
			}
			return
		}
		t.sessions[title] = &terminalSession{tmuxSession: ts, worktreePath: worktreePath}
		// Sized to the pane as it is now, not as it was when the start began.
		if (t.width != width || t.height != height) && t.width > 0 && t.height > 0 {
			if err := ts.SetDetachedSize(t.width, t.height); err != nil {
				log.InfoLog.Printf("terminal pane: failed to set size: %v", err)
			}
		}
	}()
}

// openShell starts, or takes back from a previous run, the shell session for an
// instance. It shells out to tmux and waits for the session to come up, so it
// is never called with t.mu held.
func openShell(title, worktreePath string, width, height int) (*tmux.TmuxSession, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	ts := tmux.NewTmuxSession(TerminalSessionName(title), shell)

	// Check if session already exists (e.g. from a previous run)
	if ts.DoesSessionExist() {
		if err := ts.Restore(); err != nil {
			// Session exists but can't restore, kill it and start fresh.
			// Close releases the client as well as killing the session.
			_ = ts.Close()
			ts = tmux.NewTmuxSession(TerminalSessionName(title), shell)
			if err := ts.Start(worktreePath); err != nil {
				return nil, fmt.Errorf("terminal pane: failed to start session: %w", err)
			}
		}
	} else {
		if err := ts.Start(worktreePath); err != nil {
			return nil, fmt.Errorf("terminal pane: failed to start session: %w", err)
		}
	}

	if width > 0 && height > 0 {
		if err := ts.SetDetachedSize(width, height); err != nil {
			log.InfoLog.Printf("terminal pane: failed to set size: %v", err)
		}
	}
	return ts, nil
}

// closeShell kills a shell session. Never called with t.mu held: it waits on
// tmux kill-session.
func closeShell(title string, ts *tmux.TmuxSession) {
	if err := ts.Close(); err != nil {
		log.InfoLog.Printf("terminal pane: failed to close session for %s: %v", title, err)
	}
}

// Attach attaches to the terminal tmux session (full-screen).
func (t *TerminalPane) Attach() (chan struct{}, error) {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("no terminal session to attach to")
	}
	if !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return nil, fmt.Errorf("terminal session does not exist")
	}
	ts := s.tmuxSession
	t.mu.Unlock()
	return ts.Attach()
}

// Close kills all cached terminal tmux sessions and cleans up.
func (t *TerminalPane) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for title, s := range t.sessions {
		if s.tmuxSession != nil {
			if err := s.tmuxSession.Close(); err != nil {
				log.InfoLog.Printf("terminal pane: failed to close session for %s: %v", title, err)
			}
		}
	}
	t.sessions = make(map[string]*terminalSession)
	t.currentTitle = ""
	t.content = ""
	t.fallback = false
	t.fallbackText = ""
}

// CloseForInstance drops the terminal session for a specific instance and kills
// it off the loop -- the caller is the update loop, and kill-session can wait on
// a busy tmux server. A shell still being opened for it is closed as soon as it
// is up. Should the process exit before the kill runs, the startup reaper finds
// the session by its name and removes it then.
func (t *TerminalPane) CloseForInstance(title string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.opening, title)
	if s, ok := t.sessions[title]; ok {
		if s.tmuxSession != nil {
			go closeShell(title, s.tmuxSession)
		}
		delete(t.sessions, title)
	}
	if t.currentTitle == title {
		t.currentTitle = ""
		t.content = ""
		t.fallback = false
		t.fallbackText = ""
	}
}

func (t *TerminalPane) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	width := t.width
	height := t.height

	if width == 0 || height == 0 {
		return strings.Repeat("\n", height)
	}

	if t.isScrolling {
		return t.viewport.View()
	}

	fallback := t.fallback
	fallbackText := t.fallbackText
	content := t.content

	if fallback {
		// 3 = tab bar height (border + padding + text), 4 = window style frame (top/bottom border + padding)
		availableHeight := height - 3 - 4
		fallbackLines := len(strings.Split(fallbackText, "\n"))
		totalPadding := availableHeight - fallbackLines
		topPadding := 0
		bottomPadding := 0
		if totalPadding > 0 {
			topPadding = totalPadding / 2
			bottomPadding = totalPadding - topPadding
		}

		var lines []string
		if topPadding > 0 {
			lines = append(lines, strings.Repeat("\n", topPadding))
		}
		lines = append(lines, fallbackText)
		if bottomPadding > 0 {
			lines = append(lines, strings.Repeat("\n", bottomPadding))
		}

		return terminalPaneStyle.
			Width(width).
			Align(lipgloss.Center).
			Render(strings.Join(lines, ""))
	}

	// Normal mode: show captured content.
	//
	// TrimRight before splitting: capture-pane ends its output with a newline, so
	// a split of an N-row pane yields N+1 elements, the last of them empty. The
	// tail-slice below then keeps that phantom row and drops a real one -- the
	// TOP one, which in a freshly opened terminal is the only line there is, so
	// the tab rendered blank over a shell that had already printed.
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")

	if height > 0 {
		if len(lines) > height {
			lines = lines[len(lines)-height:]
		} else {
			padding := height - len(lines)
			lines = append(lines, make([]string, padding)...)
		}
	}

	// Cut each line to the pane rather than letting it wrap. capture-pane -J
	// undoes tmux's own wrapping, so one command's output can come back far
	// wider than the pane; wrapped again here, N lines render as more than N
	// rows and the pane overflows the box drawn around it. Full lines are still
	// in the session itself, under shift-↑ or by attaching.
	for i, line := range lines {
		lines[i] = clip(line, width)
	}

	contentStr := strings.Join(lines, "\n")
	return terminalPaneStyle.Width(width).Render(contentStr)
}

// enterScrollMode captures the full terminal history and enters scroll mode.
// Caller must hold t.mu.
func (t *TerminalPane) enterScrollMode() error {
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		return nil
	}

	content, err := s.tmuxSession.CapturePaneContentWithOptions("-", "-")
	if err != nil {
		return fmt.Errorf("terminal pane: failed to capture full history: %w", err)
	}

	footer := terminalFooterStyle.Render("ESC to exit scroll mode")
	contentWithFooter := lipgloss.JoinVertical(lipgloss.Left, content, footer)
	t.viewport.SetContent(contentWithFooter)
	t.viewport.GotoBottom()
	t.isScrolling = true
	return nil
}

// ScrollUp enters scroll mode (if not already) and scrolls up.
func (t *TerminalPane) ScrollUp() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return t.enterScrollMode()
	}
	t.viewport.ScrollUp(1)
	return nil
}

// ScrollDown enters scroll mode (if not already) and scrolls down.
func (t *TerminalPane) ScrollDown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return t.enterScrollMode()
	}
	t.viewport.ScrollDown(1)
	return nil
}

// ResetToNormalMode exits scroll mode and restores normal content display.
func (t *TerminalPane) ResetToNormalMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return
	}
	t.isScrolling = false
	t.viewport.SetContent("")
	t.viewport.GotoTop()
}

// IsScrolling returns whether the terminal pane is in scroll mode.
func (t *TerminalPane) IsScrolling() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isScrolling
}
