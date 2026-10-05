package tmux

import (
	"fmt"
	cmd2 "github.com/AlexanderWeismannn/adroit/cmd"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/cmd/cmd_test"

	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"
	"github.com/muesli/ansi"
	"github.com/stretchr/testify/require"
)

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)

	// The name tmux would have stored anyway; anything else is unfindable.
	session = NewTmuxSession("fix: login", "program")
	require.Equal(t, TmuxPrefix+"fix_login", session.sanitizedName)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s adroit_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t =adroit_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}

// A tmux server that has gone away (reboot, crash, `tmux kill-server`) takes every session
// with it. attach-session against a missing session still forks successfully, so Restore
// has to check for the session itself or it reports success while attached to nothing.
func TestRestoreReturnsErrSessionNotFoundWhenSessionIsGone(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	session := NewTmuxSessionWithDeps("gone", "program", ptyFactory, cmdExec)
	err := session.Restore()

	require.ErrorIs(t, err, ErrSessionNotFound)
	require.Empty(t, ptyFactory.cmds, "should not have opened a PTY for a session that does not exist")
}

func TestRestoreAttachesWhenSessionExists(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	session := NewTmuxSessionWithDeps("alive", "program", ptyFactory, cmdExec)
	require.NoError(t, session.Restore())
	require.Len(t, ptyFactory.cmds, 1)
	require.Contains(t, ptyFactory.cmds[0].String(), "attach-session")
}

// realPtyFactory hands back genuine PTY masters. MockPtyFactory's regular files
// answer ENOTTY to the winsize ioctls, so a size assertion needs the real thing.
type realPtyFactory struct{ t *testing.T }

func (f realPtyFactory) Start(*exec.Cmd) (*os.File, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	f.t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})
	return ptmx, nil
}

func (f realPtyFactory) Close() {}

// Detach closes the PTY the UI was writing to and calls Restore to open another,
// and every PTY starts at 0x0 -- which tmux clamps to default-size, 80x24. Nothing
// re-applied the preview size afterwards, so a session came back from an attach
// 80 columns wide and stayed there until an unrelated window-size event happened
// along; its preview showed an 80x23 snapshot wrapped for the wrong width inside a
// much larger pane. The size the UI asked for has to outlive the PTY it was set on.
func TestDetachedSizeOutlivesThePtyItWasSetOn(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(*exec.Cmd) error { return nil }, // has-session succeeds
	}
	session := NewTmuxSessionWithDeps("resized", "claude", realPtyFactory{t: t}, cmdExec)

	// Asked for before any PTY exists -- the order on startup, where the UI's first
	// window-size event can land before Restore. Not a failure, just deferred.
	require.NoError(t, session.SetDetachedSize(120, 40))

	require.NoError(t, session.Restore())
	requirePtySize(t, session.ptmx, 120, 40)

	// Now the detach: a second Restore, standing in for the one Detach performs.
	first := session.ptmx
	require.NoError(t, session.Restore())
	require.NotSame(t, first, session.ptmx, "Restore should have opened a new PTY")
	requirePtySize(t, session.ptmx, 120, 40)

	// A terminal still opening reports no rows, or fewer than the chrome, and the
	// budget left over arrived here as 0 or negative. uint16(-1) sized the agent's
	// window to 65535 rows; either way the last real size is the one to keep.
	for _, size := range [][2]int{{0, 0}, {-1, -1}, {120, -2}} {
		require.NoError(t, session.SetDetachedSize(size[0], size[1]))
		requirePtySize(t, session.ptmx, 120, 40)
		require.NoError(t, session.Restore())
		requirePtySize(t, session.ptmx, 120, 40)
	}
}

func requirePtySize(t *testing.T, ptmx *os.File, wantCols, wantRows int) {
	t.Helper()
	rows, cols, err := pty.Getsize(ptmx)
	require.NoError(t, err)
	require.Equal(t, []int{wantCols, wantRows}, []int{cols, rows},
		"PTY should be at the size the UI last asked for")
}

// The footer probes are the entire basis for telling a session that has finished
// from one that has merely gone quiet, and both ways of being wrong are silent:
// a missed marker reports a busy session as finished, and a spurious one leaves a
// finished session spinning for good.
//
// The captures below are trimmed from real panes, escapes included.
// CapturePaneContent captures with -e, so a marker's own colour sits between the
// very characters being matched.
func TestClaudePaneProbe(t *testing.T) {
	const shellFooter = "\x1b[39m  \x1b[93m⏵⏵ auto mode on\x1b[37m · \x1b[96m1 shell\x1b[37m · ←  for agents\x1b[39m"
	const quietFooter = "  \x1b[93m⏵⏵ auto mode on (shift+tab to cycle)\x1b[37m · PR #4540 · ← for agents\x1b[39m"
	const workingLine = "\x1b[38;5;175m✻\x1b[39m Improvising… (3m 7s · ↓ 9.8k tokens)"
	const finishedLine = "✻ Worked for 2m 40s · done 11:28 AM"

	tests := []struct {
		name        string
		content     string
		wantWorking bool
		wantShells  int
	}{
		{
			name:        "the in-flight status line reads as working",
			content:     workingLine + "\n" + quietFooter,
			wantWorking: true,
		},
		{
			// One line reports both states, so this is the discriminator the whole
			// mechanism rests on. Reading "Worked for 2m 40s" as work in progress
			// would leave every finished session spinning.
			name:        "the finished form of the same line does not",
			content:     finishedLine + "\n" + quietFooter,
			wantWorking: false,
		},
		{
			name:        "a shorter elapsed time still reads as working",
			content:     "✢ Forming… (45s · ↓ 120 tokens)\n" + quietFooter,
			wantWorking: true,
		},
		{
			// The clock is not always first inside the parentheses: a turn in a
			// named phase says so ahead of it. Read as finished, the row stops
			// spinning and goes green while the agent is still writing.
			name:        "a phase qualifier ahead of the clock still reads as working",
			content:     "\x1b[38;5;175m✽\x1b[39m Cultivating… (running stop hook · 8m 12s · ↓ 32.8k tokens)\n" + quietFooter,
			wantWorking: true,
		},
		{
			name:        "the interrupt hint ahead of the clock still reads as working",
			content:     "✻ Deliberating… (esc to interrupt · 41s)\n" + quietFooter,
			wantWorking: true,
		},
		{
			name:       "the shell count is found through its own colour escapes",
			content:    finishedLine + "\n" + shellFooter,
			wantShells: 1,
		},
		{
			name:       "a plural count parses",
			content:    strings.Replace(shellFooter, "1 shell", "3 shells", 1),
			wantShells: 3,
		},
		{
			name:    "no segment at all means nothing is running",
			content: finishedLine + "\n" + quietFooter,
		},
		{
			// The status line floats above the input box, so an unsent draft
			// pushes it up the pane. A window tight enough for the footer would
			// lose it here and report a working session as finished.
			name: "a long unsent draft does not hide the status line",
			content: workingLine + "\n" +
				strings.Repeat("a line of a long typed-out prompt\n", paneFooterLines*2) + quietFooter,
			wantWorking: true,
		},
		{
			// Transcript prose can say anything, this sentence included. Only the
			// last few rows of the pane are the interface.
			name: "a marker in the scrollback is not the footer",
			content: "  so the footer then reads · 2 shells · and the row keeps spinning\n" +
				strings.Repeat("transcript\n", paneFooterLines) + quietFooter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			working, shells := claudePaneProbe(tt.content)
			require.Equal(t, tt.wantWorking, working)
			require.Equal(t, tt.wantShells, shells)
		})
	}
}

// The program is a command line, so an argument or an absolute path must not stop
// the probes running -- they would fail open, reporting every session finished.
func TestIsClaudeProgram(t *testing.T) {
	for _, program := range []string{"claude", "claude --resume", "/usr/local/bin/claude"} {
		require.True(t, isClaudeProgram(program), program)
	}
	for _, program := range []string{"", "aider", "echo claude"} {
		require.False(t, isClaudeProgram(program), program)
	}
}

// procExists reports whether a pid is still present as anything at all,
// including as a zombie -- which is the state this is all about.
func procExists(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// pty.Start forks a process and nothing ever called Wait on it, so every tmux
// client left a zombie behind when its PTY closed: one per attach/detach cycle,
// plus one per session started, for the life of the process. A two-hour-old
// instance had 60 of them.
func TestReapPtyCollectsAnExitedClient(t *testing.T) {
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	// It has exited (or is about to); either way this must not hang and must
	// leave nothing behind.
	reapPty(cmd, "test-session")
	require.False(t, procExists(pid), "pid %d is still present, so it was never waited for", pid)
}

// A client that does not notice its terminal closing is killed rather than
// waited for forever -- the alternative is a goroutine and a process per
// detach, held for the life of the interface.
func TestReapPtyKillsAClientThatWillNotExit(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	start := time.Now()
	reapPty(cmd, "test-session")
	require.Less(t, time.Since(start), 3*time.Second, "reaping has to be bounded")
	require.False(t, procExists(pid))
}

// The mock factory never starts anything, and neither does a session that failed
// before its PTY was opened. Reaping must be a no-op for both rather than a
// panic on a nil process.
func TestReapPtyToleratesNothingToReap(t *testing.T) {
	reapPty(nil, "test-session")
	reapPty(exec.Command("true"), "test-session") // never started
}

// A hyperlink's URI is not text on screen. tmux -e hands OSC 8 through, Claude
// Code emits one per path it prints, and PrintableRuneWidth -- what every width
// measurement in the interface is -- reads "]" as an escape terminator and
// counts the URI as printable. The overlay centres on the widest background
// line, so one of these threw the kill confirmation off to the right and then
// cut the line under it mid-escape, printing the raw URI across the box.
func TestStripTerminalStrings(t *testing.T) {
	const label = "jobs/signals/__test__/reitCounterpartyDistressSecJob.test.js"
	// Trimmed from a real capture, escapes included.
	line := "\x1b[92m●\x1b[39m \x1b[1mWrite\x1b[0m(" +
		"\x1b]8;id=1d5ebds;file:///home/developer/.adroit/worktrees/TASK-5866-BILLING_18d3f758ebc0c3db/" + label +
		"\x1b\\" + label + "\x1b]8;;\x1b\\)"

	require.Equal(t, 213, ansi.PrintableRuneWidth(line), "the raw capture is the state being fixed")

	stripped := stripTerminalStrings(line)

	require.Contains(t, stripped, label, "the visible label between the markers survives")
	require.NotContains(t, stripped, "file://", "the target does not")
	require.Equal(t, lipgloss.Width(line), ansi.PrintableRuneWidth(stripped),
		"width now agrees with what the terminal draws")
	// The colour escapes are styling, not payload, and the probes below match
	// through them.
	require.Contains(t, stripped, "\x1b[92m")
}

// A capture with nothing to strip is returned untouched -- this runs on every
// session on every tick.
func TestStripTerminalStringsLeavesPlainCapturesAlone(t *testing.T) {
	const plain = "\x1b[38;5;175m✻\x1b[39m Improvising… (3m 7s · ↓ 9.8k tokens)\n❯ "
	require.Equal(t, plain, stripTerminalStrings(plain))
}

// The launch command is how a session picked back up after a reboot runs
// `claude --resume <id>` instead of an empty `claude`. If Start kept reading
// program, the whole mechanism would be inert and the only symptom would be a
// conversation quietly starting from nothing.
func TestStartRunsTheLaunchCommandRatherThanTheProgram(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session does not exist yet")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return []byte("output"), nil },
	}

	workdir := t.TempDir()
	session := newTmuxSession("resumed", "claude", ptyFactory, cmdExec)
	session.SetLaunchCommand("claude --resume abc-123")

	require.NoError(t, session.Start(workdir))
	require.Equal(t,
		fmt.Sprintf("tmux new-session -d -s adroit_resumed -c %s claude --resume abc-123", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "claude", session.program,
		"program is also the answer to what is running in the pane, which the footer probes read")
}

func TestLaunchCommandFallsBackToTheProgram(t *testing.T) {
	session := NewTmuxSession("plain", "claude --model opus")
	require.Equal(t, "claude --model opus", session.LaunchCommand())

	session.SetLaunchCommand("claude --resume abc")
	require.Equal(t, "claude --resume abc", session.LaunchCommand())

	session.SetLaunchCommand("")
	require.Equal(t, "claude --model opus", session.LaunchCommand(),
		"clearing it gets the plain program back, for a resume whose transcript has gone")
}

// Three things can end an attach -- Ctrl-Q, a pause, and the client exiting on
// its own -- and two of them can arrive at once. Unwinding twice closes a
// closed channel, which is a panic that takes the whole interface down.
func TestDetachingTwiceIsHarmless(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{RunFunc: func(cmd *exec.Cmd) error { return nil }}

	session := NewTmuxSessionWithDeps("twice", "program", ptyFactory, cmdExec)
	require.NoError(t, session.Restore())

	ch, err := session.Attach()
	require.NoError(t, err)

	session.Detach()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("detaching did not release the caller waiting on the attach")
	}

	require.NotPanics(t, func() { session.Detach() })
	require.NoError(t, session.DetachSafely(), "and a pause arriving after it is a no-op too")
}

// Detach used to panic if it could not re-open a PTY afterwards, and a session
// whose agent has just exited has no pane to re-attach to -- so the ordinary
// end of a conversation took the interface down with it.
func TestDetachingFromASessionThatHasGoneDoesNotPanic(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	alive := true
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !alive {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	session := NewTmuxSessionWithDeps("vanishing", "program", ptyFactory, cmdExec)
	require.NoError(t, session.Restore())
	ch, err := session.Attach()
	require.NoError(t, err)

	alive = false // the agent exited while we were attached
	require.NotPanics(t, func() { session.Detach() })

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("the caller waiting on the attach was never released")
	}
}

// realProcessPtyFactory starts each command on a genuine PTY, so the sessions
// under test own real processes that can be observed leaking.
type realProcessPtyFactory struct {
	t    *testing.T
	pids []int
}

func (f *realProcessPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	// A command that outlives the test unless something closes its PTY, which
	// is exactly what a tmux client does.
	child := exec.Command("sleep", "30")
	ptmx, err := pty.Start(child)
	if err != nil {
		return nil, err
	}
	// The caller keeps the exec.Cmd it passed in and reaps that, so the process
	// this factory really started has to be the one on it.
	*cmd = *child
	f.pids = append(f.pids, child.Process.Pid)
	f.t.Cleanup(func() {
		_ = ptmx.Close()
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})
	return ptmx, nil
}

func (f *realProcessPtyFactory) Close() {}

// processState is what ps says about a pid: "Z" for a zombie, empty when it is
// gone entirely. Read through ps rather than /proc, which this codebase does
// not depend on.
func processState(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "" // ps exits non-zero for a pid that no longer exists
	}
	return strings.TrimSpace(string(out))
}

// A session owns one tmux client at a time, and Restore used to open a new one
// over the top of the old without closing it. The old master then stayed open
// until Go's finalizer got to it, at which point the client exited -- and
// nothing held a reference to wait for it, so it stayed defunct for the life of
// the process. Seven were sitting under a two-hour-old Adroit.
func TestRestoreDoesNotLeaveTheClientItReplacesBehind(t *testing.T) {
	factory := &realProcessPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{RunFunc: func(cmd *exec.Cmd) error { return nil }}

	session := NewTmuxSessionWithDeps("replaced", "program", factory, cmdExec)
	require.NoError(t, session.Restore())
	require.Len(t, factory.pids, 1)
	first := factory.pids[0]

	require.NoError(t, session.Restore())
	require.Len(t, factory.pids, 2, "a second client was opened")

	// Gone, not merely "not defunct yet". The leak has two stages: the replaced
	// client stays alive because the master keeping it open was dropped rather
	// than closed, and only when Go's finalizer eventually closes that
	// descriptor does it exit -- into a zombie, with nothing left to wait for
	// it. An assertion that only rejected the second stage would pass on the
	// first, which is the state the process spends its time in.
	require.Eventually(t, func() bool {
		return processState(t, first) == ""
	}, 5*time.Second, 25*time.Millisecond,
		"Restore left the client it replaced running, to be finalized into a zombie later")
}

// Close and the detach path go through the same release, so neither may leave
// one behind either.
func TestCloseAndDetachDoNotLeaveTheClientBehind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(*TmuxSession)
	}{
		{"Close", func(s *TmuxSession) { _ = s.Close() }},
		{"DetachSafely", func(s *TmuxSession) {
			_, err := s.Attach()
			require.NoError(t, err)
			require.NoError(t, s.DetachSafely())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := &realProcessPtyFactory{t: t}
			cmdExec := cmd_test.MockCmdExec{RunFunc: func(cmd *exec.Cmd) error { return nil }}

			session := NewTmuxSessionWithDeps("released", "program", factory, cmdExec)
			require.NoError(t, session.Restore())
			pid := factory.pids[0]

			tc.release(session)

			require.Eventually(t, func() bool {
				return processState(t, pid) == ""
			}, 5*time.Second, 25*time.Millisecond, "left its client behind")
		})
	}
}

// Releasing a session that never had a client, or releasing twice, must be a
// no-op: the terminal pane calls it on whatever it finds in its cache.
func TestCloseClientIsSafeWithNothingAttached(t *testing.T) {
	session := NewTmuxSession("empty", "program")
	require.NoError(t, session.CloseClient())
	require.NoError(t, session.CloseClient())
}

// The exact bytes the terminal sent back on a real attach, captured from the
// log. A tmux client asks these questions when it attaches; the answer comes
// back to us, after the client has stopped listening for it, and tmux passes
// what it does not recognise straight through to the pane -- so the agent's
// screen gets a line of escape sequences typed into it.
func TestTerminalRepliesAreNotForwardedToTheSession(t *testing.T) {
	observed := "\x1b[?1;2;4c\x1b[>84;0;0c\x1bP>|tmux 3.4\x1b\\"
	require.Empty(t, terminalReplies.ReplaceAllString(observed, ""))

	colour := "\x1b]10;rgb:cdcd/d6d6/f4f4\x1b\\\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\"
	require.Empty(t, terminalReplies.ReplaceAllString(colour, ""))

	// Keystrokes, and the escapes a keyboard really does send, must survive.
	// The old code discarded everything read in the first 50ms of an attach,
	// which took these with it.
	for _, keys := range []string{
		"hello",
		"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", // arrows
		"\x1bOP",                   // F1
		"\x1b[3~",                  // delete
		"\x03",                     // ctrl-c
		"\x1b[200~pasted\x1b[201~", // bracketed paste
		"\x1b[<0;10;5M",            // a mouse press
	} {
		require.Equal(t, keys, terminalReplies.ReplaceAllString(keys, ""),
			"a real keystroke was swallowed: %q", keys)
	}
}

// The capture that produced the bug, verbatim from `tmux capture-pane -p -e -J`
// over a Claude Code session with a queued prompt on screen. The prompt row sets
// a bright-black background, closes only the foreground, and leaves the
// background for the following row to turn off.
const queuedPromptCapture = "\x1b[37m✻\x1b[39m \x1b[37mCooked for 34m 9s\x1b[39m\n" +
	"\x1b[37m\x1b[100m❯ \x1b[97mgo ahead with the dry run\x1b[39m\n" +
	"\x1b[49m\n" +
	"\x1b[97m●\x1b[39m Running it.\n"

// endOfLineStyling is what a line leaves switched on once it has been read.
func endOfLineStyling(line string) *sgrState {
	var st sgrState
	for _, m := range sgrSeq.FindAllStringSubmatch(line, -1) {
		st.apply(m[1])
	}
	return &st
}

func TestSelfContainedLinesClosesStylingAtTheEndOfEveryLine(t *testing.T) {
	// Before: the prompt row hands its background to whatever is drawn next,
	// which in a composed frame is the session list in the column to its left.
	require.False(t, endOfLineStyling(strings.Split(queuedPromptCapture, "\n")[1]).isZero(),
		"fixture no longer reproduces the leak it exists to pin")

	for n, line := range strings.Split(selfContainedLines(queuedPromptCapture), "\n") {
		require.True(t, endOfLineStyling(line).isZero(), "line %d still leaks styling: %q", n, line)
	}
}

func TestSelfContainedLinesReopensStylingItInterrupted(t *testing.T) {
	// A line that relies on the previous line's colour must still get it: tmux
	// writes SGR as a delta, so closing a line without re-opening the next one
	// would silently drop the colour rather than the bleed.
	in := "\x1b[38;5;213m\x1b[1mfirst\nsecond\n\x1b[0mthird\n"
	out := strings.Split(selfContainedLines(in), "\n")

	require.Equal(t, "\x1b[1m\x1b[38;5;213msecond\x1b[0m", out[1])

	// The third line is re-opened too and then closed by the reset it already
	// carried. Redundant, not wrong: SGR is zero-width, so nothing downstream
	// measures it and the cell it precedes is drawn unstyled either way.
	require.Equal(t, "\x1b[1m\x1b[38;5;213m\x1b[0mthird", out[2])
	require.True(t, endOfLineStyling(out[2]).isZero())
}

func TestSelfContainedLinesLeavesPlainCapturesAlone(t *testing.T) {
	plain := "just text\nand more\n"
	require.Equal(t, plain, selfContainedLines(plain))
}

// isolateTmuxServer points every tmux command in the test at a private server,
// so a test can create and kill real sessions without touching the user's.
func isolateTmuxServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("", "adroit-tmux")
	require.NoError(t, err)
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
}

// A bare -t is a prefix match in tmux. With only "fix-login" alive, every
// command addressed to "fix" used to land on it: the dead session read as alive,
// previewed its neighbour's pane, and killing it killed the neighbour.
func TestCommandsForOneSessionNeverReachAnotherItPrefixes(t *testing.T) {
	isolateTmuxServer(t)
	live := toAdroitTmuxName("fix-login")
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", live, "sleep 60").Run())

	gone := newTmuxSession("fix", "claude", MakePtyFactory(), cmd2.MakeExecutor())
	require.False(t, gone.DoesSessionExist())
	_, err := gone.CapturePaneContent()
	require.Error(t, err)
	_, err = gone.CapturePaneContentWithOptions("-", "-")
	require.Error(t, err)
	// Closing a session that is already gone is a kill that got what it wanted.
	require.NoError(t, gone.Close())
	require.Error(t, KillSession(cmd2.MakeExecutor(), toAdroitTmuxName("fix")))

	require.NoError(t, exec.Command("tmux", "has-session", "-t", SessionTarget(live)).Run(),
		"the session whose name merely starts with the target must survive")

	// And the exact forms still reach the session they name.
	alive := newTmuxSession("fix-login", "claude", MakePtyFactory(), cmd2.MakeExecutor())
	require.True(t, alive.DoesSessionExist())
	_, err = alive.CapturePaneContent()
	require.NoError(t, err)
}

func TestClaudeQuestion(t *testing.T) {
	filler := strings.Repeat("some transcript line\n", 40)
	permission := filler + " Do you want to proceed?\n \x1b[36m❯\x1b[39m 1. Yes\n   2. Yes, and don't ask again\n" +
		"   3. \x1b[2mNo, and tell Claude what to do differently\x1b[22m (esc)\n\n Esc to cancel · Tab to amend"
	plan := filler + " Claude has written up a plan and is ready to execute. Would you like to proceed?\n" +
		" ❯ 1. Yes, and auto-accept edits\n   2. Yes, and manually approve edits\n   3. No, keep planning"
	question := filler + " Which database?\n ❯ 1. Postgres\n   2. Mongo\n   3. Type something.\n   4. Chat about this\n" +
		" Enter to select · ↑/↓ to navigate · Esc to cancel"

	tests := []struct {
		name                 string
		content              string
		needsInput, answered bool
	}{
		{"a permission prompt needs you, and auto-yes may answer it", permission, true, true},
		// Enter on a plan approval accepts the plan, and on a question picks the
		// first answer: both are the user's call, never auto-yes's.
		{"a plan approval needs you, and auto-yes must not answer it", plan, true, false},
		{"a question menu needs you, and auto-yes must not answer it", question, true, false},
		{"a finished turn needs nothing", filler + "✻ Worked for 2m 40s", false, false},
		// The dialog is drawn at the bottom of the pane. The same words further up
		// are transcript -- an agent describing this very feature.
		{"the phrases in the transcript above the tail are not a dialog",
			"No, and tell Claude what to do differently\nWould you like to proceed?\n" + filler + "✻ Worked for 1s", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsInput, permission := claudeQuestion(tt.content)
			require.Equal(t, tt.needsInput, needsInput)
			require.Equal(t, tt.answered, permission)
		})
	}
}

// A session that has gone is reported as gone, not as a capture that failed:
// the caller pauses it on the first, and used to log the second twice a second.
func TestPollPaneReportsASessionThatHasGone(t *testing.T) {
	isolateTmuxServer(t)
	name := toAdroitTmuxName("vanishing")
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "sleep 60").Run())
	s := newTmuxSession("vanishing", "claude", MakePtyFactory(), cmd2.MakeExecutor())
	s.monitor = newStatusMonitor()

	require.False(t, s.PollPane().Gone)
	require.NoError(t, exec.Command("tmux", "kill-session", "-t", SessionTarget(name)).Run())
	require.True(t, s.PollPane().Gone)
}
