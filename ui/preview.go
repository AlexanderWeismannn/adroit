package ui

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var previewPaneStyle, previewFooterStyle, pausedNoticeStyle lipgloss.Style
var hintKeyStyle, hintDescStyle lipgloss.Style

// hintKeyColumn is the width of the key column in the new-session hint: wide
// enough for "enter" plus the gap separating it from its description.
const hintKeyColumn = 8

func init() { theme.OnChange(applyPreviewTheme) }

func applyPreviewTheme() {
	previewPaneStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	previewFooterStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	// Warning rather than Text: the paused notice names the key that undoes it,
	// and it is the one line in the pane asking to be acted on.
	pausedNoticeStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Warning))
	hintKeyStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent)).Bold(true)
	hintDescStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
}

type PreviewPane struct {
	width  int
	height int

	previewState previewState
	isScrolling  bool
	viewport     viewport.Model

	// previewed is the instance whose content the pane is currently showing. One
	// pane is reused for every session, and two paths through UpdateContent
	// deliberately leave what is already there in place: scroll mode, whose
	// viewport is filled only on entry, and a capture that fails. Neither knew
	// the selection had moved, so in either state moving up or down the list left
	// the previous session's output on screen under the new session's name.
	//
	// Compared by pointer rather than by title -- identity is what matters here,
	// and it is exact.
	previewed *session.Instance
}

type previewState struct {
	// fallback is true if the preview pane is displaying fallback text
	fallback bool
	// text is the text displayed in the preview pane
	text string
}

func NewPreviewPane() *PreviewPane {
	return &PreviewPane{
		viewport: viewport.New(0, 0),
	}
}

func (p *PreviewPane) SetSize(width, maxHeight int) {
	width, maxHeight = max(width, 0), max(maxHeight, 0)
	p.width = width
	p.height = maxHeight
	p.viewport.Width = width
	p.viewport.Height = p.viewportHeight()
}

// viewportHeight is the rows the scrollback gets: all of them, less the status
// line pinned under it while scrolling.
func (p *PreviewPane) viewportHeight() int {
	if p.isScrolling && p.height > 1 {
		return p.height - 1
	}
	return p.height
}

// scrollStatus is the line pinned under the scrollback. It used to be a footer
// at the END of the content, which is exactly the part you scroll away from:
// once you were reading history the only sign the live view had stopped was
// gone, and it was easy to sit watching a frozen agent.
func (p *PreviewPane) scrollStatus() string {
	text := fmt.Sprintf(" scrolled · %d%% · live view paused · esc to resume ",
		int(p.viewport.ScrollPercent()*100+0.5))
	fill := p.width - lipgloss.Width(text)
	if fill >= 4 {
		text = "──" + text + strings.Repeat("─", fill-2)
	}
	return previewFooterStyle.Render(clip(text, p.width))
}

// newInstanceHint is the copy shown while a new session is being named.
//
// It used to say only "Please enter a name for the instance." — which left the
// two things worth knowing at that moment unsaid: that enter starts the session
// on a fresh branch, and that an existing branch can be chosen instead. The
// second was reachable only via N, and nothing anywhere said so.
//
// Every line is padded out to the width of the widest with U+2800 BRAILLE
// PATTERN BLANK rather than with spaces, for the reason given on FallBackText
// and one more: the pane centres this block by centring each of its lines, and
// String() strips leading and trailing SPACES first, so that a long paused
// notice cannot pad the mark above it into wrapping. Space padding therefore
// never survived to the centring step, and each row came out centred on its own
// width -- three keys down a staircase rather than a column. U+2800 is not a
// space, so it survives, and the rows centre together as one block.
func newInstanceHint() string {
	key, desc := hintKeyStyle, hintDescStyle

	row := func(k, d string) string {
		return lipgloss.JoinHorizontal(lipgloss.Left, key.Width(hintKeyColumn).Render(k), desc.Render(d))
	}

	lines := []string{
		desc.Render("Name the new session — or press tab to pick a branch:"),
		"",
		row("enter", "start it on a new branch"),
		row("tab", "pick a branch, or none — just run here"),
		row("esc", "cancel"),
	}

	width := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	for i, line := range lines {
		lines[i] += strings.Repeat(BlankCell, width-lipgloss.Width(line))
	}

	return strings.Join(lines, "\n")
}

// setFallbackState sets the preview state with fallback text and a message
func (p *PreviewPane) setFallbackState(message string) {
	p.previewState = previewState{
		fallback: true,
		text:     splash(p.width, message),
	}
}

// Updates the preview pane content with the tmux pane content
func (p *PreviewPane) UpdateContent(instance *session.Instance) error {
	// A change of selection invalidates everything the pane is holding, so deal
	// with it before anything below can decide to keep what is there. Scroll mode
	// in particular has to end here rather than at the next ESC: its viewport
	// holds the previous session's scrollback, captured once on entry, and
	// String() renders that in preference to any state we set afterwards.
	if instance != p.previewed {
		p.previewed = instance
		p.exitScrolling()
	}

	switch {
	case instance == nil:
		p.setFallbackState("No agents running yet. Spin up a new instance with 'n' to get started!")
		return nil
	case instance.Status == session.Loading:
		p.setFallbackState("Setting up workspace...")
		return nil
	case instance.Status == session.Paused:
		where := fmt.Sprintf("The instance can be checked out at '%s'", instance.Branch)
		if instance.BranchCopied() {
			where += " (copied to your clipboard)"
		}
		p.setFallbackState(lipgloss.JoinVertical(lipgloss.Center,
			"Session is paused. Press 'r' to resume.",
			"",
			pausedNoticeStyle.Render(where),
		))
		return nil
	}

	// In scroll mode the viewport owns the content: it was captured with full
	// scrollback when scroll mode was entered, and re-capturing here would drag
	// the view back to the bottom under the user's hands. The selection check
	// above guarantees that what it holds belongs to this instance.
	if p.isScrolling {
		return nil
	}

	content, err := instance.Preview()
	if err != nil {
		// Don't leave the last good capture on screen. It is this instance's
		// content, but it describes a moment that has passed, and the usual cause
		// is the tmux session having gone away entirely (a reboot, a crash,
		// `tmux kill-server`) -- which the pane should say rather than paper over
		// with output that looks live. Logged rather than returned: the preview
		// tick calls this ten times a second, and an error return rewrites the
		// error box on every one of them.
		log.ErrorLog.Printf("could not capture preview for %q: %v", instance.Title, err)
		p.setFallbackState("Could not read this session's output — its tmux session may no longer exist.")
		return nil
	}

	// Always update the preview state with content, even if empty
	// This ensures that newly created instances will display their content immediately
	if len(content) == 0 && !instance.Started() {
		p.setFallbackState(newInstanceHint())
		return nil
	}
	p.previewState = previewState{
		fallback: false,
		text:     content,
	}
	return nil
}

// ApplyCapture sets the pane's content from a capture taken elsewhere.
//
// The preview tick captures off the event loop -- a tmux capture is a
// subprocess, and doing one in Update ten times a second put blocking I/O in the
// middle of the interface's own loop -- so the tail of UpdateContent lives here,
// where the content arrives as an argument rather than being fetched.
func (p *PreviewPane) ApplyCapture(instance *session.Instance, content string) {
	if instance == nil {
		p.setFallbackState("No agents running yet. Spin up a new instance with 'n' to get started!")
		return
	}
	if instance != p.previewed {
		p.previewed = instance
		p.exitScrolling()
	}
	// The viewport owns the content in scroll mode; see UpdateContent.
	if p.isScrolling {
		return
	}
	if len(content) == 0 && !instance.Started() {
		p.setFallbackState(newInstanceHint())
		return
	}
	p.previewState = previewState{fallback: false, text: content}
}

// enterScrolling captures the instance's full scrollback into the viewport and
// switches the pane into scroll mode, positioned at the bottom where the live
// preview left off.
func (p *PreviewPane) enterScrolling(instance *session.Instance) error {
	content, err := instance.PreviewFullHistory()
	if err != nil {
		return err
	}

	p.isScrolling = true
	p.viewport.Height = p.viewportHeight()
	p.viewport.SetContent(content)
	p.viewport.GotoBottom()
	return nil
}

// exitScrolling drops scroll mode and the scrollback the viewport is holding.
// Unlike ResetToNormalMode it does not re-capture: callers either have no
// instance to capture for, or are about to capture for a different one.
func (p *PreviewPane) exitScrolling() {
	if !p.isScrolling {
		return
	}
	p.isScrolling = false
	p.viewport.Height = p.viewportHeight()
	p.viewport.SetContent("")
	p.viewport.GotoTop()
}

// Returns the preview pane content as a string.
func (p *PreviewPane) String() string {
	if p.width == 0 || p.height == 0 {
		return strings.Repeat("\n", p.height)
	}

	if p.previewState.fallback {
		// Calculate available height for fallback text
		availableHeight := p.height - 3 - 4 // 2 for borders, 1 for margin, 1 for padding

		// Count the number of lines in the fallback text
		fallbackLines := len(strings.Split(p.previewState.text, "\n"))

		// Calculate padding needed above and below to center the content
		totalPadding := availableHeight - fallbackLines
		topPadding := 0
		bottomPadding := 0
		if totalPadding > 0 {
			topPadding = totalPadding / 2
			bottomPadding = totalPadding - topPadding // accounts for odd numbers
		}

		// Build the centered content
		var lines []string
		if topPadding > 0 {
			lines = append(lines, strings.Repeat("\n", topPadding))
		}
		lines = append(lines, p.previewState.text)
		if bottomPadding > 0 {
			lines = append(lines, strings.Repeat("\n", bottomPadding))
		}

		// Centre both vertically and horizontally.
		//
		// The block's own padding is dropped first. lipgloss.JoinVertical
		// centres by padding every line out to the width of the widest one, and
		// the paused notice carries a branch name -- so against a long branch,
		// each line of the mark above it became 28 cells of braille inside a
		// hundred-odd of padding. In a pane narrower than that the padding
		// wrapped, and the mark came out double-spaced with everything below it
		// pushed down a screen. Align centres each line here anyway, so the
		// padding was never load-bearing; the mark keeps its own shape because
		// its lines are padded with U+2800, which is not a space.
		block := strings.Split(strings.Join(lines, ""), "\n")
		for i, line := range block {
			block[i] = strings.Trim(line, " ")
		}
		return previewPaneStyle.
			Width(p.width).
			Align(lipgloss.Center).
			Render(strings.Join(block, "\n"))
	}

	// If in copy mode, use the viewport to display scrollable content
	if p.isScrolling {
		return p.viewport.View() + "\n" + p.scrollStatus()
	}

	// Normal mode display.
	//
	// TrimRight before splitting: capture-pane ends its output with a newline, so
	// a split of an N-row pane yields N+1 elements, the last of them empty. That
	// phantom row was enough on its own to trip the truncation below on every
	// full-height capture. The rows it trims are blank, and the padding branch
	// puts them back.
	lines := strings.Split(strings.TrimRight(p.previewState.text, "\n"), "\n")

	// The tmux window is sized to exactly p.height rows (SetSessionPreviewSize is
	// given this pane's height), so a capture of it fits with nothing to elide.
	// Spend a row on the ellipsis only when there is genuinely more content than
	// rows: reserving it unconditionally cost the bottom line of every session
	// whose output filled the pane, and for an agent that is the line that moves --
	// the prompt, the spinner, the mode and context readout. It read as a session
	// frozen a row short of whatever it was actually doing.
	if len(lines) > p.height {
		lines = append(lines[:p.height-1], "...")
	} else {
		// Pad with empty lines to fill the pane.
		lines = append(lines, make([]string, p.height-len(lines))...)
	}

	// Cut each line to the pane rather than letting it wrap, as the terminal and
	// run panes already do. capture-pane -J undoes tmux's own wrapping, so a line
	// the session wrapped comes back as one long one; wrapped again here, N lines
	// render as more than N rows, the pane overflows the box drawn around it, and
	// the composed view is cut to height -- taking the window's bottom border with
	// it, and with it the rows the agent is actually writing to.
	for i, line := range lines {
		lines[i] = clip(line, p.width)
	}

	content := strings.Join(lines, "\n")
	rendered := previewPaneStyle.Width(p.width).Render(content)
	return rendered
}

// ScrollUp scrolls up in the viewport
func (p *PreviewPane) ScrollUp(instance *session.Instance) error {
	if instance == nil || instance.Status == session.Paused {
		return nil
	}

	if !p.isScrolling {
		return p.enterScrolling(instance)
	}

	// Already in scroll mode, just scroll the viewport
	p.viewport.LineUp(1)
	return nil
}

// ScrollDown scrolls down in the viewport
func (p *PreviewPane) ScrollDown(instance *session.Instance) error {
	if instance == nil || instance.Status == session.Paused {
		return nil
	}

	if !p.isScrolling {
		return p.enterScrolling(instance)
	}

	// Already in copy mode, just scroll the viewport
	p.viewport.LineDown(1)
	return nil
}

// ResetToNormalMode exits scroll mode and returns to normal mode
func (p *PreviewPane) ResetToNormalMode(instance *session.Instance) error {
	if instance == nil || instance.Status == session.Paused {
		return nil
	}

	if !p.isScrolling {
		return nil
	}
	p.exitScrolling()

	// Re-capture now instead of waiting for the next tick, so leaving scroll mode
	// does not flash the scrollback for a frame. Through UpdateContent rather than
	// assigning previewState.text directly: that left the fallback flag as scroll
	// mode found it, so exiting onto a session that had been showing a fallback
	// rendered the fresh capture centred as though it were the fallback message.
	return p.UpdateContent(instance)
}
