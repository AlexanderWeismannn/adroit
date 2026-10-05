package ui

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd/cmd_test"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// testSetup holds common test setup data
type testSetup struct {
	workdir     string
	instance    *session.Instance
	sessionName string
	cleanupFn   func()
}

// setupTestEnvironment creates a common test environment with git repo and instance
func setupTestEnvironment(t *testing.T, cmdExec cmd_test.MockCmdExec) *testSetup {
	t.Helper()

	// Initialize logging
	log.Initialize(false)

	// Set up a temp working directory
	workdir := t.TempDir()

	// Initialize git repository
	setupGitRepo(t, workdir)

	// Create unique session name
	random := time.Now().UnixNano() % 10000000
	sessionName := fmt.Sprintf("test-preview-%s-%d-%d", t.Name(), time.Now().UnixNano(), random)

	// Clean up any existing tmux session
	cleanupCmd := exec.Command("tmux", "kill-session", "-t", "adroit_"+sessionName)
	_ = cleanupCmd.Run() // Ignore errors if session doesn't exist

	// Create instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   sessionName,
		Path:    workdir,
		Program: "bash",
		AutoYes: false,
	})
	require.NoError(t, err)

	// Create MockPtyFactory
	ptyFactory := &MockPtyFactory{
		t:       t,
		cmdExec: cmdExec,
	}

	// Set up tmux session with mocks
	tmuxSession := tmux.NewTmuxSessionWithDeps(sessionName, "bash", ptyFactory, cmdExec)
	instance.SetTmuxSession(tmuxSession)

	// Start the tmux session
	err = instance.Start(true)
	require.NoError(t, err)

	// Create cleanup function
	cleanupFn := func() {
		if instance != nil {
			_ = instance.Kill() // Ignore errors during cleanup
		}
		log.Close()
	}

	return &testSetup{
		workdir:     workdir,
		instance:    instance,
		sessionName: sessionName,
		cleanupFn:   cleanupFn,
	}
}

// setupGitRepo initializes a git repository in the given directory
func setupGitRepo(t *testing.T, workdir string) {
	t.Helper()

	// Initialize git repository
	initCmd := exec.Command("git", "init")
	initCmd.Dir = workdir
	err := initCmd.Run()
	require.NoError(t, err)

	// Create basic git config (local to this repo only)
	configCmd := exec.Command("git", "config", "--local", "user.email", "test@example.com")
	configCmd.Dir = workdir
	err = configCmd.Run()
	require.NoError(t, err)

	configCmd = exec.Command("git", "config", "--local", "user.name", "Test User")
	configCmd.Dir = workdir
	err = configCmd.Run()
	require.NoError(t, err)

	// Create and commit a test file
	testFile := filepath.Join(workdir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	addCmd := exec.Command("git", "add", "test.txt")
	addCmd.Dir = workdir
	err = addCmd.Run()
	require.NoError(t, err)

	commitCmd := exec.Command("git", "commit", "-m", "initial commit")
	commitCmd.Dir = workdir
	err = commitCmd.Run()
	require.NoError(t, err)
}

// TestPreviewScrolling tests the scrolling functionality in the preview pane
func TestPreviewScrolling(t *testing.T) {
	// Track what commands were executed and their order
	var executedCommands []string
	inCopyMode := false
	scrollPosition := 0 // 0 = bottom, positive = scrolled up
	sessionCreated := false

	// Create test content with line numbers for scrolling
	const numLines = 100
	lines := make([]string, numLines+1)
	lines[0] = "$ seq 100" // Command that was run
	for i := 1; i <= numLines; i++ {
		lines[i] = fmt.Sprintf("%d", i)
	}
	fullContent := strings.Join(lines, "\n")

	// Mock command execution
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			cmdStr := cmd.String()
			executedCommands = append(executedCommands, cmdStr)

			// Handle tmux session creation and existence checking
			if strings.Contains(cmdStr, "has-session") {
				if sessionCreated {
					return nil // Session exists
				} else {
					return fmt.Errorf("session does not exist")
				}
			}

			// Handle session creation
			if strings.Contains(cmdStr, "new-session") {
				sessionCreated = true
				return nil
			}

			// Handle attach-session
			if strings.Contains(cmdStr, "attach-session") {
				return nil
			}

			// Handle copy mode commands
			if strings.Contains(cmdStr, "copy-mode") {
				inCopyMode = true
			}
			if strings.Contains(cmdStr, "send-keys") && strings.Contains(cmdStr, "q") {
				inCopyMode = false
				scrollPosition = 0 // Reset position when exiting copy mode
			}
			if strings.Contains(cmdStr, "send-keys") && strings.Contains(cmdStr, "Up") {
				if inCopyMode {
					scrollPosition++
				}
			}
			if strings.Contains(cmdStr, "send-keys") && strings.Contains(cmdStr, "Down") {
				if inCopyMode && scrollPosition > 0 {
					scrollPosition--
				}
			}

			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			cmdStr := cmd.String()

			// Handle capture-pane commands
			if strings.Contains(cmdStr, "capture-pane") {
				// Check if this is a request for cursor position
				if strings.Contains(cmdStr, "display-message") && strings.Contains(cmdStr, "copy_cursor_y") {
					var buf []byte
					buf = fmt.Appendf(buf, "%d", scrollPosition)
					return buf, nil
				}

				// Check if this is a copy mode capture with full history (-S -)
				if strings.Contains(cmdStr, "-S -") {
					// Always return the full content for PreviewFullHistory
					return []byte(fullContent), nil
				}

				// Regular capture for normal preview mode - show the last 20 lines
				const visibleLines = 20
				startLine := max(0, numLines+1-visibleLines)
				visibleContent := strings.Join(lines[startLine:], "\n")
				return []byte(visibleContent), nil
			}

			return []byte(""), nil
		},
	}

	// Setup test environment
	setup := setupTestEnvironment(t, cmdExec)
	defer setup.cleanupFn()

	// Simulate running a command that produces lots of output
	err := setup.instance.SendKeys("seq 100")
	require.NoError(t, err)
	err = setup.instance.SendKeys("") // Simulate pressing Enter
	require.NoError(t, err)

	// Create the preview pane
	previewPane := NewPreviewPane()
	previewPane.SetSize(80, 30) // Set reasonable size for testing

	// Step 1: Check initial content - should show normal preview mode
	err = previewPane.UpdateContent(setup.instance)
	require.NoError(t, err)

	// Verify we're not in scrolling mode initially
	require.False(t, previewPane.isScrolling, "Should not be in scrolling mode initially")

	// Step 2: Check that PreviewFullHistory returns all content
	fullHistory, err := setup.instance.PreviewFullHistory()
	require.NoError(t, err)

	// Verify that the full history contains both the command and early output
	require.Contains(t, fullHistory, "$ seq 100", "Full history should contain the command")
	require.Contains(t, fullHistory, "1", "Full history should contain earliest output")

	// Step 3: Enter scroll mode
	err = previewPane.ScrollUp(setup.instance)
	require.NoError(t, err)

	// Verify we entered scrolling mode
	require.True(t, previewPane.isScrolling, "Should be in scrolling mode after ScrollUp")

	// Step 4: Get the content directly from the viewport
	viewportContent := previewPane.viewport.View()
	t.Logf("Viewport content: %q", viewportContent)

	// With proper implementation, the viewport should have the full history content
	// Note: The viewport will be positioned at the bottom initially, so we need to scroll up

	// Step 5: Scroll up multiple times to get to the top
	for range 50 {
		err = previewPane.ScrollUp(setup.instance)
		require.NoError(t, err)
	}

	// Now get the viewport content after scrolling up
	viewportAfterScrollUp := previewPane.viewport.View()
	t.Logf("Viewport after scrolling up: %q", viewportAfterScrollUp)

	// Step 6: Scroll down multiple times
	for range 25 {
		err = previewPane.ScrollDown(setup.instance)
		require.NoError(t, err)
	}

	// Get updated viewport content after scrolling down
	viewportAfterScrollDown := previewPane.viewport.View()
	t.Logf("Viewport after scrolling down: %q", viewportAfterScrollDown)

	// Step 7: Reset to normal mode
	err = previewPane.ResetToNormalMode(setup.instance)
	require.NoError(t, err)

	// Verify we exited scrolling mode
	require.False(t, previewPane.isScrolling, "Should not be in scrolling mode after reset")
}

// MockPtyFactory for testing tmux sessions
type MockPtyFactory struct {
	t       *testing.T
	cmdExec cmd_test.MockCmdExec

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), len(pt.cmds)))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)

		// Execute the command through our mock to trigger session creation logic
		_ = pt.cmdExec.Run(cmd)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

// TestPreviewContentWithoutScrolling tests that the preview pane correctly displays content
// for a new instance without requiring scrolling
func TestPreviewContentWithoutScrolling(t *testing.T) {
	// Create test content
	expectedContent := "$ echo test\ntest"

	// Track session creation state
	sessionCreated := false

	// Mock command execution
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			cmdStr := cmd.String()

			// Handle tmux session creation and existence checking
			if strings.Contains(cmdStr, "has-session") {
				if sessionCreated {
					return nil // Session exists
				} else {
					return fmt.Errorf("session does not exist")
				}
			}

			// Handle session creation
			if strings.Contains(cmdStr, "new-session") {
				sessionCreated = true
				return nil
			}

			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			cmdStr := cmd.String()

			// Handle capture-pane commands for normal preview
			if strings.Contains(cmdStr, "capture-pane") {
				// Return our test content for normal preview
				return []byte(expectedContent), nil
			}

			return []byte(""), nil
		},
	}

	// Setup test environment
	setup := setupTestEnvironment(t, cmdExec)
	defer setup.cleanupFn()

	// Create the preview pane
	previewPane := NewPreviewPane()
	previewPane.SetSize(80, 30) // Set reasonable size for testing

	// Update the preview content (this should display the content without scrolling)
	err := previewPane.UpdateContent(setup.instance)
	require.NoError(t, err)

	// Verify we're not in scrolling mode
	require.False(t, previewPane.isScrolling, "Should not be in scrolling mode")

	// Verify that the preview state is not in fallback mode
	require.False(t, previewPane.previewState.fallback, "Preview should not be in fallback mode")

	// Verify that the preview state contains the expected content
	require.Equal(t, expectedContent, previewPane.previewState.text, "Preview state should contain the expected content")

	// Verify the rendered string contains the content
	renderedString := previewPane.String()
	require.Contains(t, renderedString, "test", "Rendered preview should contain the test content")
}

// Helper function for max
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// startedInstance builds a Running instance backed by a mocked tmux session, with
// no git worktree and no real tmux involved: output answers every capture-pane, so
// the pane sees exactly the text a test hands it. Start(false) is the restore path,
// which is why nothing here needs a repository.
func startedInstance(t *testing.T, title string, output func(cmdStr string) ([]byte, error)) *session.Instance {
	t.Helper()

	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil }, // has-session succeeds
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) { return output(cmd.String()) },
	}

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps(
		title, "claude", &MockPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
	require.NoError(t, instance.Start(false))
	require.True(t, instance.Started())

	return instance
}

// capturing answers every capture-pane with the same text.
func capturing(text string) func(string) ([]byte, error) {
	return func(cmdStr string) ([]byte, error) {
		if !strings.Contains(cmdStr, "capture-pane") {
			return []byte(""), nil
		}
		return []byte(text), nil
	}
}

// One pane serves every session, and scroll mode used to survive a change of
// selection: its viewport is filled once, on entry, and String() renders it in
// preference to anything UpdateContent sets. Moving up or down the list therefore
// left the previous session's scrollback on screen under the new session's name.
func TestSwitchingInstanceLeavesScrollModeAndShowsTheNewSession(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	first := startedInstance(t, "first-session", capturing("output from FIRST"))
	second := startedInstance(t, "second-session", capturing("output from SECOND"))

	p := NewPreviewPane()
	p.SetSize(80, 20)

	require.NoError(t, p.UpdateContent(first))
	require.NoError(t, p.ScrollUp(first))
	require.True(t, p.isScrolling, "ScrollUp should have entered scroll mode")
	require.Contains(t, p.String(), "FIRST")

	require.NoError(t, p.UpdateContent(second))
	require.False(t, p.isScrolling, "a change of selection must leave scroll mode")

	rendered := p.String()
	require.Contains(t, rendered, "SECOND", "the pane should show the newly selected session")
	require.NotContains(t, rendered, "FIRST", "the previous session's scrollback must be gone")
}

// A capture can fail for a reason that outlives the tick -- most often the tmux
// session is gone (a reboot, a crash, `tmux kill-server`). Returning early left the
// last good capture rendering, so a dead session showed whichever session had been
// selected before it, looking live.
func TestFailedCaptureDoesNotLeaveThePreviousSessionOnScreen(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	live := startedInstance(t, "live-session", capturing("output from LIVE"))
	dead := startedInstance(t, "dead-session", func(cmdStr string) ([]byte, error) {
		if strings.Contains(cmdStr, "capture-pane") {
			return nil, fmt.Errorf("exit status 1")
		}
		return []byte(""), nil
	})

	p := NewPreviewPane()
	p.SetSize(80, 20)

	require.NoError(t, p.UpdateContent(live))
	require.Contains(t, p.String(), "LIVE")

	// Reported in the pane rather than returned: the preview tick calls this ten
	// times a second, and an error return rewrites the error box on every one.
	require.NoError(t, p.UpdateContent(dead))

	rendered := p.String()
	require.NotContains(t, rendered, "LIVE", "the previous session's output must not stand in for a failed capture")
	require.Contains(t, rendered, "may no longer exist", "the pane should say why it has nothing to show")
}

// The tmux window is sized to exactly the pane's height, so its capture always
// fits. Two things conspired to truncate it anyway: capture-pane ends with a
// newline, which splits into a phantom final element, and a row was reserved for
// the ellipsis whether or not anything was elided. The bottom row lost to that is
// the one that moves in an agent session -- the prompt, the spinner, the mode and
// context readout -- so a busy session read as frozen a row short of what it was
// actually doing.
func TestFullHeightCaptureKeepsItsBottomRow(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	const height = 20
	rows := make([]string, height)
	for i := range rows {
		rows[i] = fmt.Sprintf("row-%d", i+1)
	}
	// What tmux hands back for a pane this tall: one line per row, trailing newline.
	instance := startedInstance(t, "full-pane", capturing(strings.Join(rows, "\n")+"\n"))

	p := NewPreviewPane()
	p.SetSize(80, height)
	require.NoError(t, p.UpdateContent(instance))

	rendered := p.String()
	require.Contains(t, rendered, "row-20", "the bottom row of the pane must survive")
	require.NotContains(t, rendered, "...", "a capture that fits the pane has nothing to elide")
	require.Equal(t, height, len(strings.Split(rendered, "\n")), "the pane should fill its height exactly")
}

// Content genuinely taller than the pane still gets an ellipsis, and still fills
// the pane exactly -- the row it takes comes out of the content, not out of the
// pane's height.
func TestOverlongCaptureIsElidedWithinThePaneHeight(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	const height = 10
	rows := make([]string, height*2)
	for i := range rows {
		rows[i] = fmt.Sprintf("row-%d", i+1)
	}
	instance := startedInstance(t, "tall-pane", capturing(strings.Join(rows, "\n")+"\n"))

	p := NewPreviewPane()
	p.SetSize(80, height)
	require.NoError(t, p.UpdateContent(instance))

	lines := strings.Split(p.String(), "\n")
	require.Len(t, lines, height)
	require.Contains(t, lines[height-1], "...", "the last row should say there is more")
}

// A line wider than the pane is cut, not wrapped. capture-pane -J undoes tmux's
// own wrapping, so a wrapped line comes back as one long one; re-wrapped here it
// renders as two rows, the pane overflows the box drawn around it, and the
// composed view is cut to height -- taking the window's bottom border with it.
func TestALineWiderThanThePaneIsClippedNotWrapped(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	const width, height = 50, 12
	rows := []string{strings.Repeat("a very long joined line ", 10), "second", "third"}
	instance := startedInstance(t, "wide-line", capturing(strings.Join(rows, "\n")+"\n"))

	p := NewPreviewPane()
	p.SetSize(width, height)
	require.NoError(t, p.UpdateContent(instance))

	lines := strings.Split(p.String(), "\n")
	require.Len(t, lines, height, "one row per captured line, whatever their length")
	for i, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), width, "row %d overruns the pane", i)
	}
	require.Contains(t, p.String(), "second", "the lines below it are still there")
}

// The paused notice carries a branch name, and lipgloss.JoinVertical centres by
// padding every line out to the widest one -- so against a long branch each line
// of the mark became 28 cells of braille inside a hundred of padding. In a pane
// narrower than that, the padding wrapped: the mark came out double-spaced and
// the message was pushed off the bottom.
func TestALongPausedNoticeDoesNotDoubleSpaceTheSplash(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	const width, height = 50, 30
	instance := startedInstance(t, "paused-pane", capturing(""))
	instance.Status = session.Paused
	instance.Branch = "developer/task-5756_companyinfo-dedup-5-6-one-active-catalog-row-per-id-service-id"

	p := NewPreviewPane()
	p.SetSize(width, height)
	require.NoError(t, p.UpdateContent(instance))

	lines := strings.Split(p.String(), "\n")
	for i, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), width, "row %d overruns the pane", i)
	}
	// Consecutive rows of the mark, with no blank row wrapped in between them.
	marks := 0
	for i, line := range lines {
		if strings.Contains(line, "\u2800") && i+1 < len(lines) && strings.Contains(lines[i+1], "\u2800") {
			marks++
		}
	}
	require.GreaterOrEqual(t, marks, 5, "the mark should render as consecutive rows:\n%s", p.String())
	require.Contains(t, p.String(), "paused", "and the notice below it still fits")
}

// The mark is 28 cells wide and nothing wraps a picture usefully: in a narrower
// pane every second row was the overflow of the row above it, and the message
// that actually says something was pushed off the bottom.
func TestTheSplashDropsItsMarkInAPaneTooNarrowForIt(t *testing.T) {
	const message = "No agents running yet."

	require.Equal(t, message, splash(20, message), "no room for the mark")
	require.NotContains(t, splash(20, message), "\u2800")

	wide := splash(lipgloss.Width(FallBackText), message)
	require.Contains(t, wide, "\u2800", "at its own width the mark is drawn")
	require.Contains(t, wide, message)
}

// The pane centres its fallback text by centring each LINE, and strips leading
// and trailing spaces first so a long paused notice cannot pad the mark above it
// into wrapping. Space padding therefore never reached the centring step, and
// each hint row was centred on its own width -- three keys down a staircase
// instead of a column, with the longest row sticking out furthest left.
func TestNewSessionHintKeysLineUpInAColumn(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "unnamed-session",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	require.False(t, instance.Started(), "the hint is what an unstarted session shows")

	p := NewPreviewPane()
	p.SetSize(80, 24)
	p.ApplyCapture(instance, "")

	var keyColumns []int
	var widths []int
	for _, line := range strings.Split(p.String(), "\n") {
		plain := stripANSI(line)
		var key string
		switch {
		case strings.Contains(plain, "start it on a new branch"):
			key = "enter"
		case strings.Contains(plain, "just run here"):
			key = "tab"
		case strings.Contains(plain, "cancel"):
			key = "esc"
		default:
			continue
		}
		keyColumns = append(keyColumns, lipgloss.Width(plain[:strings.Index(plain, key)]))
		widths = append(widths, lipgloss.Width(strings.TrimRight(plain, " ")))
	}

	require.Len(t, keyColumns, 3, "all three hint rows should be on screen")
	require.Equal(t, []int{keyColumns[0], keyColumns[0], keyColumns[0]}, keyColumns,
		"every key should start in the same column")
	require.Equal(t, []int{widths[0], widths[0], widths[0]}, widths,
		"the rows are one block, so they should be padded to a common width before centring")
}

// While scrolled, the pane says so on a line of its own that does not scroll
// away, and the pane stays exactly its height.
func TestScrolledPreviewPinsItsStatus(t *testing.T) {
	p := NewPreviewPane()
	p.SetSize(60, 10)
	p.isScrolling = true
	p.viewport.Height = p.viewportHeight()
	p.viewport.SetContent(strings.Repeat("history\n", 100))
	p.viewport.GotoTop()

	rows := strings.Split(p.String(), "\n")
	require.Len(t, rows, 10)
	require.Contains(t, rows[len(rows)-1], "live view paused")
	require.Contains(t, rows[len(rows)-1], "0%")
}

// A terminal that is still coming up reports a size too small to hold the
// chrome -- Windows Terminal opens at a handful of rows and tmux at 0x0 before
// either settles -- and the height budget left for the pane went negative. With
// a capture to show, the elision then sliced lines[:-2] and panicked on the very
// first frame, which is how Adroit died on launch in a fresh window.
func TestThePaneRendersAtAnySizeWithoutPanicking(t *testing.T) {
	log.Initialize(false)
	defer log.Close()

	instance := startedInstance(t, "tiny", capturing("one\ntwo\nthree\nfour\n"))
	for _, w := range []int{-3, 0, 1, 2, 10} {
		for _, h := range []int{-3, -1, 0, 1, 2, 3} {
			p := NewPreviewPane()
			p.SetSize(w, h)
			require.NotPanics(t, func() {
				_ = p.UpdateContent(instance)
				_ = p.String()
			}, "%dx%d", w, h)
		}
	}
}
