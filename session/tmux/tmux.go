package tmux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/log"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

const ProgramClaude = "claude"

const ProgramAider = "aider"
const ProgramGemini = "gemini"

// TmuxSession represents a managed tmux session
type TmuxSession struct {
	// Initialized by NewTmuxSession
	//
	// The name of the tmux session and the sanitized name used for tmux commands.
	sanitizedName string
	// captureFailing is whether the last status capture failed, so a capture
	// that keeps failing is logged once rather than twice a second.
	captureFailing bool
	program        string
	// launchCommand is what Start actually runs, when that differs from program.
	//
	// program stays the user's own command, because it is also the answer to
	// "what is running in this pane" -- which the footer probes and the auto-yes
	// prompt matching are written against, two of them by string equality. A
	// session picked back up after a reboot runs `claude --resume <uuid>`, and
	// folding that into program would turn those features off for exactly the
	// sessions that had been through a restart.
	launchCommand string
	// ptyFactory is used to create a PTY for the tmux session.
	ptyFactory PtyFactory
	// cmdExec is used to execute commands in the tmux session.
	cmdExec cmd.Executor

	// Initialized by Start or Restore
	//
	// ptmx is a PTY is running the tmux attach command. This can be resized to change the
	// stdout dimensions of the tmux pane. On detach, we close it and set a new one.
	// This should never be nil.
	ptmx *os.File
	// ptyCmd is the process whose PTY ptmx is: a `tmux attach-session` client.
	//
	// Kept so it can be waited for. pty.Start forks a process and nothing here
	// ever called Wait on it, so every client left a zombie behind when its PTY
	// was closed -- one per attach/detach cycle, plus one per session started, for
	// the life of the process. Measured on a two-hour-old instance: 60 defunct
	// children.
	ptyCmd *exec.Cmd
	// monitor monitors the tmux pane content and sends signals to the UI when it's status changes
	monitor *statusMonitor
	// requestedCols/requestedRows are the shape the UI last asked for while detached.
	// Every PTY we open starts at 0x0, which tmux clamps to default-size (80x24), and
	// nothing re-applies the size afterwards -- so a session whose PTY was replaced
	// (Restore after a detach, on resume, on startup) came back 80 columns by 23 rows
	// and stayed there until something else happened to emit a window-size event. Its
	// preview then showed an 80x23 snapshot wrapped for the wrong width inside a much
	// larger pane, which reads as a session that has stopped making sense rather than
	// one that is merely mis-sized.
	requestedCols, requestedRows int

	// Initialized by Attach
	// Deinitilaized by Detach
	//
	// Channel to be closed at the very end of detaching. Used to signal callers.
	// Nil means "not attached", and claiming it is what makes detaching
	// idempotent -- it can now be reached from three directions (Ctrl-Q, a pause,
	// and the client exiting on its own) and must unwind exactly once.
	attachCh chan struct{}
	// stdinState is the terminal mode to put back on detach.
	//
	// While attached, this session is a proxy: the tmux client runs on a PTY of
	// our making, not on the real terminal, so whatever the user types has to
	// arrive here as raw bytes to be forwarded. Bubbletea used to be holding the
	// terminal raw throughout -- badly, since it was also reading it -- and now
	// that it hands the terminal over, nobody is. A cooked terminal line-buffers
	// input, so nothing reaches the session until Enter, echoes it locally over
	// whatever the session is drawing, and swallows Ctrl-Q as XON flow control:
	// the detach key stops working entirely.
	stdinState *term.State
	// stdin is the input pump's reader: os.Stdin wrapped so a blocked read can be
	// interrupted. os.Stdin.Read blocks until a key arrives, so a pump that only
	// checks for detach between reads sits inside one long after the detach and
	// swallows the next keystroke -- which by then belongs to the interface, not
	// to the session.
	stdin cancelreader.CancelReader
	// While attached, we use some goroutines to manage the window size and stdin/stdout. This stuff
	// is used to terminate them on Detach. We don't want them to outlive the attached window.
	ctx    context.Context
	cancel func()
	wg     *sync.WaitGroup
	// detachMu makes claiming attachCh atomic. Two of the three things that can
	// end an attach can arrive together -- the client exiting and the user
	// pressing Ctrl-Q on the same instant -- and both unwinding would close a
	// closed channel.
	detachMu sync.Mutex
}

const TmuxPrefix = "adroit_"

// ErrSessionNotFound is returned when the tmux session backing an instance is gone, which
// happens whenever the tmux server dies (reboot, crash, `tmux kill-server`).
var ErrSessionNotFound = errors.New("tmux session no longer exists")

var whiteSpaceRegex = regexp.MustCompile(`\s+`)

func toAdroitTmuxName(str string) string {
	str = whiteSpaceRegex.ReplaceAllString(str, "")
	str = strings.ReplaceAll(str, ".", "_") // tmux replaces all . with _
	return fmt.Sprintf("%s%s", TmuxPrefix, str)
}

// NewTmuxSession creates a new TmuxSession with the given name and program.
func NewTmuxSession(name string, program string) *TmuxSession {
	return newTmuxSession(name, program, MakePtyFactory(), cmd.MakeExecutor())
}

// NewTmuxSessionWithDeps creates a new TmuxSession with provided dependencies for testing.
func NewTmuxSessionWithDeps(name string, program string, ptyFactory PtyFactory, cmdExec cmd.Executor) *TmuxSession {
	return newTmuxSession(name, program, ptyFactory, cmdExec)
}

func newTmuxSession(name string, program string, ptyFactory PtyFactory, cmdExec cmd.Executor) *TmuxSession {
	return &TmuxSession{
		sanitizedName: toAdroitTmuxName(name),
		program:       program,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
	}
}

// ptyReapGrace is how long a client gets to notice its PTY has closed before it
// is killed outright.
const ptyReapGrace = 500 * time.Millisecond

// reapPty waits for the process whose PTY has just been closed, so it does not
// linger as a zombie.
//
// Closing the master end is what tells a tmux client to exit, and it normally
// does so at once; a client that does not is killed rather than waited for
// forever. Call it on a goroutine -- nothing in the interface should block on
// another process noticing anything.
func reapPty(cmd *exec.Cmd, name string) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(ptyReapGrace):
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(ptyReapGrace):
		log.WarningLog.Printf("tmux client for %s did not exit after its PTY was closed", name)
	}
}

// CloseClient shuts down the PTY and the tmux client on the other end of it, if
// there is one. A session with no client is left alone, so it is safe to call
// on any session at any time.
//
// A TmuxSession owns at most one client, and replacing one without closing it
// does not merely leak a descriptor. The master end is what keeps the client
// alive, so a dropped *os.File stays open until Go's finalizer gets to it, at
// which point the client exits -- and the only code that would have waited for
// it no longer holds a reference, so it exits into a zombie. Restore did
// exactly that on every call, which is why a two-hour-old Adroit was sitting on
// seven defunct tmux clients.
func (t *TmuxSession) CloseClient() error {
	var err error
	if t.ptmx != nil {
		if closeErr := t.ptmx.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = fmt.Errorf("error closing PTY for %s: %w", t.sanitizedName, closeErr)
		}
		t.ptmx = nil
	}
	// Closing the master is what tells the client to exit, so the reap has to
	// come after it. On a goroutine: nothing in this interface should block on
	// another process noticing anything.
	if t.ptyCmd != nil {
		go reapPty(t.ptyCmd, t.sanitizedName)
		t.ptyCmd = nil
	}
	return err
}

// Start creates and starts a new tmux session, then attaches to it. Program is the command to run in
// the session (ex. claude). workdir is the git worktree directory.
func (t *TmuxSession) Start(workDir string) error {
	// Check if the session already exists
	if t.DoesSessionExist() {
		return fmt.Errorf("tmux session already exists: %s", t.sanitizedName)
	}

	// Create a new detached tmux session and start claude in it
	cmd := exec.Command("tmux", "new-session", "-d", "-s", t.sanitizedName, "-c", workDir, t.LaunchCommand())

	ptmx, err := t.ptyFactory.Start(cmd)
	if err != nil {
		// Cleanup any partially created session if any exists.
		if t.DoesSessionExist() {
			cleanupCmd := exec.Command("tmux", "kill-session", "-t", SessionTarget(t.sanitizedName))
			if cleanupErr := t.cmdExec.Run(cleanupCmd); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("error starting tmux session: %w", err)
	}

	// Poll for session existence with exponential backoff
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !t.DoesSessionExist() {
		select {
		case <-timeout:
			if cleanupErr := t.Close(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("timed out waiting for tmux session %s: %v", t.sanitizedName, err)
		default:
			time.Sleep(sleepDuration)
			// Exponential backoff up to 50ms max
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}
	ptmx.Close()
	// `new-session -d` has done its work and exited; without this it stays a
	// zombie for the life of the process.
	go reapPty(cmd, t.sanitizedName)

	// Set history limit to enable scrollback (default is 2000, we'll use 10000 for more history)
	historyCmd := exec.Command("tmux", "set-option", "-t", PaneTarget(t.sanitizedName), "history-limit", "10000")
	if err := t.cmdExec.Run(historyCmd); err != nil {
		log.InfoLog.Printf("Warning: failed to set history-limit for session %s: %v", t.sanitizedName, err)
	}

	// Enable mouse scrolling for the session
	mouseCmd := exec.Command("tmux", "set-option", "-t", PaneTarget(t.sanitizedName), "mouse", "on")
	if err := t.cmdExec.Run(mouseCmd); err != nil {
		log.InfoLog.Printf("Warning: failed to enable mouse scrolling for session %s: %v", t.sanitizedName, err)
	}

	err = t.Restore()
	if err != nil {
		if cleanupErr := t.Close(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("error restoring tmux session: %w", err)
	}

	return nil
}

// SetLaunchCommand sets the command line Start runs, overriding program.
//
// Empty clears it, which is what a caller does when it wants the plain program
// back -- a resume whose transcript has since been deleted, say.
func (t *TmuxSession) SetLaunchCommand(command string) {
	t.launchCommand = command
}

// LaunchCommand is what Start will run.
func (t *TmuxSession) LaunchCommand() string {
	if t.launchCommand != "" {
		return t.launchCommand
	}
	return t.program
}

// CheckAndHandleTrustPrompt checks the pane content once for a trust prompt and dismisses it if found.
// Returns true if the prompt was found and handled.
func (t *TmuxSession) CheckAndHandleTrustPrompt() bool {
	content, err := t.CapturePaneContent()
	if err != nil {
		return false
	}

	if strings.HasSuffix(t.program, ProgramClaude) {
		if strings.Contains(content, "Do you trust the files in this folder?") ||
			strings.Contains(content, "new MCP server") {
			if err := t.TapEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust/MCP screen: %v", err)
			}
			return true
		}
	} else {
		if strings.Contains(content, "Open documentation url for more info") {
			if err := t.TapDAndEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust screen: %v", err)
			}
			return true
		}
	}
	return false
}

// Restore attaches to an existing session and restores the window size
func (t *TmuxSession) Restore() error {
	// Whatever was attached before is replaced below, so let go of it first.
	// Unconditionally, and before the existence check: a Restore that finds the
	// session gone is the clearest case of all, since the client it would have
	// orphaned is attached to nothing.
	if err := t.CloseClient(); err != nil {
		log.WarningLog.Printf("%v", err)
	}

	// attach-session against a missing session still forks a process successfully, so the
	// PTY start below would report no error while leaving us attached to nothing. Check
	// first so callers can tell "session is gone" apart from "PTY failed".
	if !t.DoesSessionExist() {
		return ErrSessionNotFound
	}

	attach := exec.Command("tmux", "attach-session", "-t", SessionTarget(t.sanitizedName))
	ptmx, err := t.ptyFactory.Start(attach)
	if err != nil {
		return fmt.Errorf("error opening PTY: %w", err)
	}
	t.ptmx = ptmx
	t.ptyCmd = attach
	t.monitor = newStatusMonitor()
	// Re-apply the size the UI last asked for. monitorWindowSize deliberately does
	// not go through SetDetachedSize, so the real-terminal size used while attached
	// never becomes the remembered one and this puts the pane back to preview shape.
	if t.requestedCols > 0 && t.requestedRows > 0 {
		if err := t.updateWindowSize(t.requestedCols, t.requestedRows); err != nil {
			log.ErrorLog.Printf("could not restore detached size for %s: %v", t.sanitizedName, err)
		}
	}
	return nil
}

type statusMonitor struct {
	// Store hashes to save memory.
	prevOutputHash []byte
}

func newStatusMonitor() *statusMonitor {
	return &statusMonitor{}
}

// hash hashes the string.
func (m *statusMonitor) hash(s string) []byte {
	h := sha256.New()
	// TODO: this allocation sucks since the string is probably large. Ideally, we hash the string directly.
	h.Write([]byte(s))
	return h.Sum(nil)
}

// TapEnter sends an enter keystroke to the tmux pane.
func (t *TmuxSession) TapEnter() error {
	_, err := t.ptmx.Write([]byte{0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

// TapDAndEnter sends 'D' followed by an enter keystroke to the tmux pane.
func (t *TmuxSession) TapDAndEnter() error {
	_, err := t.ptmx.Write([]byte{0x44, 0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

func (t *TmuxSession) SendKeys(keys string) error {
	_, err := t.ptmx.Write([]byte(keys))
	return err
}

// PaneState is everything one capture of the pane says about the session.
//
// A struct rather than a longer list of return values: what a capture can tell
// us has grown past the point where the caller can be trusted to keep three
// bare bools in the right order.
type PaneState struct {
	// Updated reports that the pane content changed since the previous capture.
	// The fallback activity signal, for programs whose interface we cannot read.
	Updated bool
	// HasPrompt reports a confirmation prompt that auto-yes can answer.
	HasPrompt bool
	// Gone reports that the capture failed because the tmux session no longer
	// exists -- the agent exited, or someone killed the session. Every other field
	// is then zero, and meaningless.
	Gone bool
	// NeedsInput reports that the agent is blocked on something only the user can
	// answer: a permission prompt, a plan waiting for approval, a question with
	// options. A superset of HasPrompt -- auto-yes presses enter on a permission
	// prompt, but must never pick a plan or an answer on the user's behalf.
	NeedsInput bool
	// Legible reports that the running program's interface is one the fields
	// below were read off, rather than left at their zero values. Only Claude
	// Code qualifies today, because it states both of them outright.
	Legible bool
	// Working reports that the agent is mid-turn.
	Working bool
	// BackgroundShells is how many shells the agent has left running behind it.
	//
	// Non-zero with Working false is the state this whole mechanism exists for:
	// the agent has ended its turn, the pane has gone still, and it will wake up
	// and carry on by itself the moment one of these exits. Read by the hash
	// alone that session is indistinguishable from a finished one.
	BackgroundShells int
}

// How far up from the bottom of the pane each probe reads. The window is the
// guard, not the pattern: both markers are short enough that transcript prose
// above them will eventually produce one by accident -- a model describing this
// very feature writes "· 2 shells" into the scrollback.
//
// They differ because the two markers sit in different places. The footer is the
// pane's last line, always. The status line floats above the input box, which
// grows with whatever has been typed into it, so a long unsent draft pushes it
// further up -- and a window tight enough for the footer would lose it, reporting
// a working session as finished.
const (
	paneFooterLines = 6
	paneStatusLines = 32
	// paneQuestionLines covers the options block of a dialog, which is drawn at
	// the bottom of the pane with at most a two-line hint beneath it. The dialog's
	// own body -- a diff, a plan -- sits above and can be any length, which is why
	// the probes below match the options rather than the heading.
	paneQuestionLines = 16
)

// Phrases from the dialogs Claude Code draws when it cannot go on without you,
// taken from the strings the CLI ships. Each is an option or a question the
// dialog alone prints, so prose that merely mentions one in the transcript would
// have to land in the bottom lines of the pane to be mistaken for it.
const (
	// The last option of every permission prompt: "No, and tell Claude what to
	// do differently (esc)". Also the only one auto-yes may answer.
	claudePermissionOption = "No, and tell Claude what to do differently"
	// The same option in the variant that says "Deny".
	claudeDenyOption = "and tell Claude what to do differently"
	// The plan-approval dialog's question.
	claudePlanQuestion = "Would you like to proceed?"
	// The last option of an AskUserQuestion menu.
	claudeQuestionOption = "Chat about this"
)

// claudeQuestion reports whether a capture ends in one of those dialogs, and
// whether it is the permission prompt auto-yes is allowed to answer.
func claudeQuestion(content string) (needsInput, permission bool) {
	tail := paneTail(content, paneQuestionLines)
	permission = strings.Contains(tail, claudePermissionOption)
	needsInput = permission ||
		strings.Contains(tail, claudeDenyOption) ||
		strings.Contains(tail, claudePlanQuestion) ||
		strings.Contains(tail, claudeQuestionOption)
	return needsInput, permission
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// paneTail returns the last paneTailLines lines of a capture with the colour
// escapes stripped.
//
// Stripping is not optional. CapturePaneContent captures with -e, so a marker's
// own styling sits between the very characters being matched: the footer arrives
// as "\x1b[37m · \x1b[96m1 shell\x1b[37m · ", and a pattern written against
// what the terminal displays misses it every time.
func paneTail(content string, n int) string {
	lines := strings.Split(ansiSeq.ReplaceAllString(content, ""), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// claudeWorking matches Claude Code's in-flight status line: a gerund, an
// ellipsis, and the elapsed time in parentheses --
// "✻ Improvising… (3m 7s · ↓ 9.8k tokens)".
//
// The parenthesised duration is what makes the line safe to look for. The
// finished form, "✻ Worked for 2m 40s · done 11:28 AM", carries neither the
// ellipsis nor the parentheses, so the one line that reports both states cannot
// be read as the wrong one.
//
// The duration is not always the first thing in the parentheses. Claude Code
// puts a qualifier ahead of it whenever the turn is in a phase worth naming --
// "✽ Cultivating… (running stop hook · 8m 12s · ↓ 32.8k tokens)", "(esc to
// interrupt · 41s)" -- and a pattern anchored on the opening bracket reads
// exactly those sessions as finished: the row stops spinning and goes green
// mid-turn. So one bullet-terminated segment may precede the clock.
var claudeWorking = regexp.MustCompile(`…\s+\((?:[^()\n]*?·\s+)?(?:\d+h\s+)?(?:\d+m\s+)?\d+s\b`)

// claudeShells matches the background-shell count Claude Code prints in its
// footer: "⏵⏵ auto mode on · 1 shell · ← for agents". The segment is absent
// entirely when nothing is running.
var claudeShells = regexp.MustCompile(`·\s+(\d+)\s+shells?\b`)

// isClaudeProgram reports whether the pane is running Claude Code, whose footer
// the probes above know how to read.
//
// It tests the executable rather than the whole command, because the program is
// a command line: "claude --resume" and "/usr/local/bin/claude" are both Claude
// Code. The trust-prompt check still takes a suffix of the whole command line,
// which no argument survives; it only dismisses the first-run screens, so it is
// left as it is.
func isClaudeProgram(program string) bool {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return false
	}
	return strings.HasSuffix(fields[0], ProgramClaude)
}

// claudePaneProbe reads the two footer facts off a capture: whether the agent is
// mid-turn, and how many shells it has left running.
//
// Pure, and separate from PollPane, because every way it can be wrong is silent.
// A missed marker reports a busy session as finished; a spurious one leaves a
// finished session spinning for good.
func claudePaneProbe(content string) (working bool, backgroundShells int) {
	working = claudeWorking.MatchString(paneTail(content, paneStatusLines))
	if m := claudeShells.FindStringSubmatch(paneTail(content, paneFooterLines)); m != nil {
		// An unparseable count still means at least one shell. The segment is only
		// drawn when something is running, so reading it as zero would report the
		// session finished -- the one answer that must never be guessed.
		backgroundShells = 1
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			backgroundShells = n
		}
	}
	return working, backgroundShells
}

// PollPane captures the pane once and reports everything that capture says.
//
// Deliberately one capture per call: the content hash, the prompt match and the
// footer probes all have to describe the same instant, and this runs on the
// metadata tick for every active session.
func (t *TmuxSession) PollPane() PaneState {
	content, err := t.CapturePaneContent()
	if err != nil {
		// A session that has gone is a state to report, not an error to repeat:
		// this runs twice a second, and logging it each time is what used to
		// bury everything else in the log until the next restart noticed.
		if !t.DoesSessionExist() {
			return PaneState{Gone: true}
		}
		if !t.captureFailing {
			log.ErrorLog.Printf("error capturing pane content in status monitor: %v", err)
		}
		t.captureFailing = true
		return PaneState{}
	}
	t.captureFailing = false

	var state PaneState

	// Claude is recognised by its executable, not the whole command line, so a
	// restored "claude --resume <id>" is still read; the other programs keep
	// their original checks. aider and gemini have no probe that tells a prompt
	// auto-yes can answer from any other question, so for them the two coincide.
	if isClaudeProgram(t.program) {
		state.Legible = true
		state.Working, state.BackgroundShells = claudePaneProbe(content)
		state.NeedsInput, state.HasPrompt = claudeQuestion(content)
	} else if strings.HasPrefix(t.program, ProgramAider) {
		state.HasPrompt = strings.Contains(content, "(Y)es/(N)o/(D)on't ask again")
		state.NeedsInput = state.HasPrompt
	} else if strings.HasPrefix(t.program, ProgramGemini) {
		state.HasPrompt = strings.Contains(content, "Yes, allow once")
		state.NeedsInput = state.HasPrompt
	}

	// Hashed once, not once per comparison: the capture is tens of kilobytes and
	// this runs per session per tick.
	if h := t.monitor.hash(content); !bytes.Equal(h, t.monitor.prevOutputHash) {
		t.monitor.prevOutputHash = h
		state.Updated = true
	}

	return state
}

// stdinReadErrLimit bounds how many consecutive stdin read failures the input
// pump tolerates before it gives up.
//
// The loop used to `continue` on every error that was not EOF, which is a busy
// spin for any error that does not clear -- and the one that matters does not.
// A controlling terminal that has gone away fails every read with EIO, forever:
// that is the attached session at 100% CPU responding to nothing.
const stdinReadErrLimit = 8

// uncancelableStdin is the fallback for a stdin that cannot be wrapped in a
// cancelable reader.
//
// cancelreader polls the descriptor, and not everything that can be a process's
// stdin can be polled -- /dev/null under a test runner is the common one. That
// is not a reason to refuse to attach: without cancellation the pump simply
// unblocks on the next keystroke instead of at once, which is how it behaved
// before. Close is deliberately a no-op; the real stdin is not ours to close.
type uncancelableStdin struct{ *os.File }

func (uncancelableStdin) Cancel() bool { return false }

func (uncancelableStdin) Close() error { return nil }

// openStdin wraps os.Stdin so a blocked read can be interrupted, degrading to a
// plain read when it cannot.
func openStdin() cancelreader.CancelReader {
	reader, err := cancelreader.NewReader(os.Stdin)
	if err == nil {
		return reader
	}
	log.WarningLog.Printf(
		"stdin cannot be interrupted (%v); a detach will not take effect until the next keystroke", err)
	return uncancelableStdin{os.Stdin}
}

func (t *TmuxSession) Attach() (chan struct{}, error) {
	// The caller has handed us the terminal for the duration (see
	// app.withTerminalReleased), so stdin is ours alone to read -- and ours to
	// put into raw mode, since the release put it back the way the shell had it.
	if state, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		t.stdinState = state
	} else {
		log.WarningLog.Printf(
			"could not put the terminal into raw mode to attach to %s (%v); Ctrl-Q may not detach",
			t.sanitizedName, err)
	}

	stdin := openStdin()

	attachCh := make(chan struct{})
	t.attachCh = attachCh
	t.stdin = stdin

	t.wg = &sync.WaitGroup{}
	t.wg.Add(1)
	t.ctx, t.cancel = context.WithCancel(context.Background())

	// Everything the goroutines need is taken now, not read off the session as
	// they go. The detach clears those fields and opens a fresh set for the next
	// attach, so a goroutine that outlives its own attach by an instant would
	// otherwise read a nil context, or write this attach's leftover keystrokes
	// into the next one's PTY.
	ctx, wg, ptmx := t.ctx, t.wg, t.ptmx

	// Size the pane before the pumps start. The output pump can reach the
	// unwind on its very first read -- a client that was never going to come up
	// -- and the unwind clears the fields this reads.
	t.monitorWindowSize()

	// The output pump terminates when the ptmx is closed. It is in the waitgroup,
	// so nothing that waits on the waitgroup may run on it.
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, ptmx)
		// io.Copy returning means the connection closed. The context says which
		// kind of closing it was.
		select {
		case <-ctx.Done():
			// A deliberate detach, which is doing the unwinding itself.
		default:
			// The client went away on its own: the program in the pane exited, or
			// the tmux server died. Nobody is going to press Ctrl-Q now, so the
			// attach has to end itself -- otherwise the caller's <-attachCh never
			// returns and the interface stays wedged in the attached state for the
			// life of the process, which is exactly the state that survives a
			// terminal window being closed. On its own goroutine, and without
			// waiting, because the unwind waits for this one.
			fmt.Fprintf(os.Stderr, "\n\033[31mError: Session terminated without detaching. Use Ctrl-Q to properly detach from tmux sessions.\033[0m\n")
			go func() { _ = t.detach(false, true) }()
		}
	}()

	go t.pumpStdin(stdin, ptmx, ctx)

	// The local channel, not the field: by now the unwind may already have run
	// and cleared it, and returning nil would leave the caller blocked on a nil
	// channel for the life of the process.
	return attachCh, nil
}

// terminalReplies matches the answers a terminal sends back when it is asked
// what it is: primary and secondary device attributes, the XTVERSION/DECRPSS
// string reply, an OSC colour report, and a cursor-position report.
//
// They have to be filtered out of what is forwarded. A tmux client asks these
// questions the moment it attaches, and while we are proxying, the question
// goes out through our stdout to the real terminal and the answer comes back on
// our stdin -- arriving after the client has stopped listening for it, at which
// point tmux passes it through to the pane and the agent's screen gets
// "^[[?1;2;4c^[[>84;0;0c^[P>|tmux 3.4^[\\" typed into it.
//
// The old code dealt with this by discarding everything read in the first 50ms
// of an attach, which also discarded whatever the user typed in that window.
// Matching the replies themselves costs nothing and keeps the keystrokes: no
// keyboard produces these sequences.
var terminalReplies = regexp.MustCompile(
	`\x1b\[\?[0-9;]*c` + // primary device attributes
		`|\x1b\[>[0-9;]*c` + // secondary device attributes
		`|\x1bP[>!][|+][^\x1b]*(?:\x1b\\)?` + // XTVERSION / DECRPSS
		`|\x1b\][0-9]+;rgb:[0-9a-fA-F/]*(?:\x07|\x1b\\)?` + // OSC colour report
		`|\x1b\[[0-9]+;[0-9]+R`) // cursor position report

// replyWindow is how long after an attach the filter above applies.
//
// Bounded rather than permanent: the questions are asked at attach time and
// nowhere else, so a match later in a session is far more likely to be a paste
// of text that happens to contain an escape sequence than a reply nobody asked
// for.
const replyWindow = 2 * time.Second

// pumpStdin forwards the user's keystrokes to the attached client until the
// detach.
//
// ptmx and ctx are arguments rather than fields: a detach replaces both, and a
// pump that re-read them would start writing this session's leftover keystrokes
// into the PTY the next attach opened.
func (t *TmuxSession) pumpStdin(stdin io.Reader, ptmx io.Writer, ctx context.Context) {
	// Large enough to hold a terminal's reply in one read. The replies run to
	// forty-odd bytes, and one split across two reads would defeat the filter
	// below and be forwarded in halves.
	buf := make([]byte, 512)
	failures := 0
	attachedAt := time.Now()
	for {
		nr, err := stdin.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, cancelreader.ErrCanceled) {
				return
			}
			failures++
			if failures >= stdinReadErrLimit {
				log.ErrorLog.Printf(
					"giving up reading stdin for %s after %d consecutive failures: %v",
					t.sanitizedName, failures, err)
				return
			}
			continue
		}
		failures = 0

		// Nothing read after the detach may be forwarded: those bytes belong to
		// whatever has the terminal now.
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Ctrl+Q (ASCII 17) detaches.
		if nr == 1 && buf[0] == 17 {
			t.Detach()
			return
		}

		input := buf[:nr]
		if time.Since(attachedAt) < replyWindow {
			// Not logged: it happens on nearly every attach, by design, and was
			// an eighth of the log.
			if filtered := terminalReplies.ReplaceAll(input, nil); len(filtered) != len(input) {
				input = filtered
			}
			if len(input) == 0 {
				continue
			}
		}

		if _, err := ptmx.Write(input); err != nil {
			// The PTY is gone, which means the attach is over one way or another.
			if !errors.Is(err, os.ErrClosed) {
				log.WarningLog.Printf("could not forward input to %s: %v", t.sanitizedName, err)
			}
			return
		}
	}
}

// DetachSafely disconnects from the current tmux session, leaving no client
// behind it.
//
// Used by pause, which is about to remove the worktree the session is sitting
// in, so it deliberately does not re-open a detached PTY afterwards: resume
// opens one when it puts the worktree back.
func (t *TmuxSession) DetachSafely() error {
	return t.detach(true, false)
}

// Detach disconnects from the current tmux session and leaves a detached PTY in
// place, so the pane can go on being previewed and resized.
func (t *TmuxSession) Detach() {
	if err := t.detach(true, true); err != nil {
		log.ErrorLog.Printf("errors while detaching from %s: %v", t.sanitizedName, err)
	}
}

// detach unwinds an attach exactly once.
//
// wait says whether to wait for the goroutines the attach started, which the
// output pump itself must not do -- it is one of them. restore says whether to
// leave a detached PTY behind.
//
// It reports its errors rather than panicking. Detach used to panic if the PTY
// would not close or if re-opening one failed, and the second of those is not a
// programming error at all: a session whose agent has just exited has no pane to
// re-attach to, so the ordinary end of a conversation took down the interface
// and every other session's attach with it.
func (t *TmuxSession) detach(wait, restore bool) error {
	// Claiming the channel is what makes this idempotent. Three things can end an
	// attach -- Ctrl-Q, a pause, and the client exiting on its own -- and two of
	// them can happen at once, so the claim is taken under a lock.
	t.detachMu.Lock()
	ch := t.attachCh
	t.attachCh = nil
	t.detachMu.Unlock()
	if ch == nil {
		return nil
	}

	var errs []error

	// Cancel first. Everything below closes something the goroutines are reading,
	// and the context is how they tell a deliberate close from a client that
	// died; cancelling afterwards leaves that a race, which is what made a clean
	// detach print "Session terminated without detaching" now and then.
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}

	if t.stdin != nil {
		t.stdin.Cancel()
		if err := t.stdin.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing stdin reader: %w", err))
		}
		t.stdin = nil
	}

	// Hand the terminal back the way it was found. The caller restores
	// bubbletea's own mode on top of this once the attach has returned; leaving
	// it raw here would mean a failed attach left the user's shell raw.
	if t.stdinState != nil {
		if err := term.Restore(int(os.Stdin.Fd()), t.stdinState); err != nil {
			errs = append(errs, fmt.Errorf("error restoring the terminal mode: %w", err))
		}
		t.stdinState = nil
	}

	// Closing the master end is what tells the client to exit. Done before the
	// wait below, and before any Restore, so the client is on its way out while
	// the pumps unwind.
	if err := t.CloseClient(); err != nil {
		errs = append(errs, err)
	}

	wg := t.wg
	t.wg = nil
	t.ctx = nil
	if wait && wg != nil {
		wg.Wait()
	}

	if restore {
		if err := t.Restore(); err != nil {
			// Not an error the caller can do anything about, and not a reason to
			// fail the detach: a session that has gone away has no PTY to re-open,
			// and the next poll parks the instance as paused.
			log.WarningLog.Printf("could not re-open a detached PTY for %s: %v", t.sanitizedName, err)
		}
	}

	// Last, so a caller woken by this finds the teardown finished.
	close(ch)

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("errors during detach: %v", errs)
}

// killSessionTimeout bounds the kill-session that Close issues.
const killSessionTimeout = 10 * time.Second

// Close terminates the tmux session and cleans up resources
func (t *TmuxSession) Close() error {
	var errs []error

	if err := t.CloseClient(); err != nil {
		errs = append(errs, err)
	}

	// Bounded for the same reason the git teardown is: Close runs on a kill, and
	// a tmux server that has stopped answering must not be able to hold the
	// session's removal open indefinitely. A kill-session that takes this long is
	// not going to succeed later either.
	ctx, cancel := context.WithTimeout(context.Background(), killSessionTimeout)
	defer cancel()
	killCmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", SessionTarget(t.sanitizedName))
	if err := t.cmdExec.Run(killCmd); err != nil {
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			errs = append(errs, fmt.Errorf("error killing tmux session: timed out after %s", killSessionTimeout))
		case cmd.IsNoSuchSession(err):
			// Already gone -- the agent exited, or tmux was restarted. That is
			// the outcome a kill wants, not a teardown that failed.
		default:
			errs = append(errs, fmt.Errorf("error killing tmux session: %w", err))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple errors occurred during cleanup:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return errors.New(errMsg)
}

// SetDetachedSize set the width and height of the session while detached. This makes the
// tmux output conform to the specified shape.
func (t *TmuxSession) SetDetachedSize(width, height int) error {
	// A size of nothing is a terminal still opening, not a request: keep the
	// last real one, here and in the PTY. Remembering it would have Restore open
	// the next PTY at nothing.
	if width < 1 || height < 1 {
		return nil
	}
	t.requestedCols, t.requestedRows = width, height
	// With no PTY there is nothing to resize yet, and that is not a failure: the
	// request is remembered above and Restore applies it as soon as one exists.
	if t.ptmx == nil {
		return nil
	}
	return t.updateWindowSize(width, height)
}

// updateWindowSize updates the window size of the PTY.
func (t *TmuxSession) updateWindowSize(cols, rows int) error {
	if t.ptmx == nil {
		return fmt.Errorf("no PTY for session %s", t.sanitizedName)
	}
	// Never size a PTY to nothing, and never to a negative size: uint16(-1)
	// resized the agent's window to 65535 rows.
	if cols < 1 || rows < 1 {
		return nil
	}
	return pty.Setsize(t.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
		X:    0,
		Y:    0,
	})
}

func (t *TmuxSession) DoesSessionExist() bool {
	existsCmd := exec.Command("tmux", "has-session", "-t", SessionTarget(t.sanitizedName))
	return t.cmdExec.Run(existsCmd) == nil
}

// terminalStrings matches the escapes that carry a payload rather than a
// styling change: OSC, DCS, APC, PM and SOS, terminated by BEL or ST.
//
// tmux -e hands these straight through, and Claude Code emits one per file it
// prints -- a path is an OSC 8 hyperlink, whose target sits in the stream ahead
// of the label. Every width measurement in the interface is muesli's
// PrintableRuneWidth, which treats "]" as an escape terminator, so the URI is
// counted as text on screen: one captured line measured 213 cells wide against
// the 69 it actually occupies.
//
// Nothing downstream can survive that. The overlay centres itself on the widest
// background line, so one hyperlink threw the kill confirmation off to the
// right; the same arithmetic then cut the line under it mid-escape, printing the
// raw URI across the box. Stripping here, at the one place this content enters
// the program, keeps every consumer measuring what the terminal will draw. The
// label between the pair of OSC 8 markers is the visible text and stays.
var terminalStrings = regexp.MustCompile("\x1b[\\]P^_X][^\x07\x1b]*(?:\x07|\x1b\\\\)?")

// stripTerminalStrings removes those escapes from a capture.
func stripTerminalStrings(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return terminalStrings.ReplaceAllString(s, "")
}

// sgrSeq matches a Select Graphic Rendition escape -- the colour and attribute
// changes that capture-pane -e preserves. Deliberately narrower than ansiSeq:
// this is the only escape class whose effect outlives the character it is
// attached to, and so the only one whose state has to be tracked across lines.
var sgrSeq = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// sgrState is the styling a capture has left switched on at some point in the
// stream: at most one foreground, at most one background, and the attribute
// flags. Colours are kept as the escape that set them so an indexed or truecolour
// selection is re-stated exactly as tmux wrote it.
type sgrState struct {
	fg, bg string
	attrs  []int
}

// attrOn is the attribute codes worth carrying; attrOff maps each disabling code
// to the ones it clears. Anything outside both is left alone -- tmux emits
// nothing else, and inventing state for an unknown code would re-state it wrongly
// on every following line.
var (
	attrOn  = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 21: true, 53: true}
	attrOff = map[int][]int{22: {1, 2}, 23: {3}, 24: {4, 21}, 25: {5, 6}, 27: {7}, 28: {8}, 29: {9}, 55: {53}}
)

func (s *sgrState) isZero() bool { return s.fg == "" && s.bg == "" && len(s.attrs) == 0 }

// open renders the state as the escapes that re-establish it.
func (s *sgrState) open() string {
	if s.isZero() {
		return ""
	}
	var b strings.Builder
	for _, a := range s.attrs {
		fmt.Fprintf(&b, "\x1b[%dm", a)
	}
	b.WriteString(s.fg)
	b.WriteString(s.bg)
	return b.String()
}

// apply folds one SGR escape's parameters into the state.
func (s *sgrState) apply(params string) {
	codes := make([]int, 0, 8)
	for _, p := range strings.Split(params, ";") {
		n, err := strconv.Atoi(p)
		if p == "" {
			n, err = 0, nil // an empty parameter is 0, and "\x1b[m" is "\x1b[0m"
		}
		if err != nil {
			return // a parameter we cannot read makes the whole escape unsafe to model
		}
		codes = append(codes, n)
	}
	for i := 0; i < len(codes); i++ {
		c := codes[i]
		switch {
		case c == 0:
			*s = sgrState{}
		case c == 38 || c == 48:
			n := extendedColorLen(codes[i:])
			seq := "\x1b[" + params38(codes[i:i+n]) + "m"
			if c == 38 {
				s.fg = seq
			} else {
				s.bg = seq
			}
			i += n - 1
		case c >= 30 && c <= 37, c >= 90 && c <= 97:
			s.fg = fmt.Sprintf("\x1b[%dm", c)
		case c == 39:
			s.fg = ""
		case c >= 40 && c <= 47, c >= 100 && c <= 107:
			s.bg = fmt.Sprintf("\x1b[%dm", c)
		case c == 49:
			s.bg = ""
		default:
			s.setAttr(c)
		}
	}
}

func (s *sgrState) setAttr(c int) {
	if off, ok := attrOff[c]; ok {
		kept := s.attrs[:0]
		for _, a := range s.attrs {
			cleared := false
			for _, o := range off {
				if a == o {
					cleared = true
				}
			}
			if !cleared {
				kept = append(kept, a)
			}
		}
		s.attrs = kept
		return
	}
	if !attrOn[c] {
		return
	}
	for _, a := range s.attrs {
		if a == c {
			return
		}
	}
	s.attrs = append(s.attrs, c)
}

// extendedColorLen is how many parameters the 38/48 selection at the head of
// codes consumes: 5;n for indexed, 2;r;g;b for truecolour. A malformed one
// consumes just itself, which leaves the rest to be read as ordinary codes --
// wrong, but bounded, where guessing a length is not.
func extendedColorLen(codes []int) int {
	if len(codes) < 2 {
		return 1
	}
	switch codes[1] {
	case 5:
		if len(codes) >= 3 {
			return 3
		}
	case 2:
		if len(codes) >= 5 {
			return 5
		}
	}
	return 1
}

func params38(codes []int) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ";")
}

// selfContainedLines makes every line of a capture stand on its own: the styling
// it inherits is re-stated at its start, and whatever is still switched on at its
// end is closed.
//
// tmux emits SGR as a delta across the whole capture. A line that ends inside a
// highlight simply leaves the highlight on and lets the next line turn it off --
// correct for a stream written straight to a terminal, and wrong for anything
// that renders those lines somewhere other than column zero. The preview pane is
// the right-hand column of a composed frame, so the row after a line that left a
// background on begins with the SESSION LIST, drawn in it. Claude Code's queued
// prompt ("❯ ...", bright-black background) ends exactly that way, which is why
// the smear appeared at random: it needed a prompt sitting in the pane at the
// moment of capture.
//
// It also did not go away. bubbletea rewrites only the rows that changed, so the
// left-hand column kept the background until those rows happened to move.
//
// Cheap by construction: SGR escapes are zero-width, so no width measurement
// anywhere downstream sees a difference, and the status matchers strip escapes
// before matching.
func selfContainedLines(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	lines := strings.Split(s, "\n")
	var st sgrState
	for i, line := range lines {
		open := st.open()
		for _, m := range sgrSeq.FindAllStringSubmatch(line, -1) {
			st.apply(m[1])
		}
		if open == "" && st.isZero() {
			continue
		}
		if !st.isZero() {
			line += "\x1b[0m"
		}
		lines[i] = open + line
	}
	return strings.Join(lines, "\n")
}

// normalizeCapture is everything a capture needs on the way in: the escapes that
// carry a payload removed, and the styling that remains confined to its own line.
func normalizeCapture(s string) string {
	return selfContainedLines(stripTerminalStrings(s))
}

// CapturePaneContent captures the content of the tmux pane
func (t *TmuxSession) CapturePaneContent() (string, error) {
	// Add -e flag to preserve escape sequences (ANSI color codes)
	cmd := exec.Command("tmux", "capture-pane", "-p", "-e", "-J", "-t", PaneTarget(t.sanitizedName))
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("error capturing pane content: %v", err)
	}
	return normalizeCapture(string(output)), nil
}

// CapturePaneContentWithOptions captures the pane content with additional options
// start and end specify the starting and ending line numbers (use "-" for the start/end of history)
func (t *TmuxSession) CapturePaneContentWithOptions(start, end string) (string, error) {
	// Add -e flag to preserve escape sequences (ANSI color codes)
	cmd := exec.Command("tmux", "capture-pane", "-p", "-e", "-J", "-S", start, "-E", end, "-t", PaneTarget(t.sanitizedName))
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to capture tmux pane content with options: %v", err)
	}
	return normalizeCapture(string(output)), nil
}

// SessionNameFor is the tmux session a session of this name runs in.
//
// Exported so a caller can recognise the sessions this package created without
// re-deriving the naming and drifting from it.
func SessionNameFor(name string) string {
	return toAdroitTmuxName(name)
}

// ListSessionNames returns every tmux session this package owns.
func ListSessionNames(cmdExec cmd.Executor) ([]string, error) {
	output, err := cmdExec.Output(exec.Command("tmux", "list-sessions", "-F", "#{session_name}"))
	if err != nil {
		// Exit 1 is "no server running", which is no sessions rather than a fault.
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list tmux sessions: %w", err)
	}

	var names []string
	for _, line := range strings.Split(string(output), "\n") {
		if name := strings.TrimSpace(line); strings.HasPrefix(name, TmuxPrefix) {
			names = append(names, name)
		}
	}
	return names, nil
}

// SessionTarget is the -t argument that names exactly one session. A bare name
// is a PREFIX match in tmux: with only "adroit_fix-login" alive, "-t adroit_fix"
// resolves to it, so a dead session would be captured, attached and killed as its
// longer-named neighbour. The leading "=" makes the match exact.
func SessionTarget(name string) string { return "=" + name }

// PaneTarget is SessionTarget for commands that take a pane or window target
// (capture-pane, set-option, list-panes). Those parse "session:window.pane", and
// an exact session with no trailing colon is not recognised as a session at all.
func PaneTarget(name string) string { return "=" + name + ":" }

// KillSession kills a tmux session by its full name.
func KillSession(cmdExec cmd.Executor, name string) error {
	return cmdExec.Run(exec.Command("tmux", "kill-session", "-t", SessionTarget(name)))
}

// CleanupSessions kills all tmux sessions that start with "session-"
func CleanupSessions(cmdExec cmd.Executor) error {
	// First try to list sessions
	cmd := exec.Command("tmux", "ls")
	output, err := cmdExec.Output(cmd)

	// If there's an error and it's because no server is running, that's fine
	// Exit code 1 typically means no sessions exist
	if err != nil {
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil // No sessions to clean up
		}
		return fmt.Errorf("failed to list tmux sessions: %v", err)
	}

	re := regexp.MustCompile(fmt.Sprintf(`%s.*:`, TmuxPrefix))
	matches := re.FindAllString(string(output), -1)
	for i, match := range matches {
		matches[i] = match[:strings.Index(match, ":")]
	}

	for _, match := range matches {
		log.InfoLog.Printf("cleaning up session: %s", match)
		if err := cmdExec.Run(exec.Command("tmux", "kill-session", "-t", SessionTarget(match))); err != nil {
			return fmt.Errorf("failed to kill tmux session %s: %v", match, err)
		}
	}
	return nil
}
