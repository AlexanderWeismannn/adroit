package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/theme"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/ansi"
	"github.com/muesli/reflow/truncate"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var (
	runPaneStyle    lipgloss.Style
	runHeaderStyle  lipgloss.Style
	runPhaseStyle   lipgloss.Style
	runUpStyle      lipgloss.Style
	runPendingStyle lipgloss.Style
	runDownStyle    lipgloss.Style
	runDetailStyle  lipgloss.Style
	runRuleStyle    lipgloss.Style
)

func init() { theme.OnChange(applyRunTheme) }

func applyRunTheme() {
	runPaneStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	runHeaderStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text)).Bold(true)
	runPhaseStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	runUpStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	runPendingStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Warning))
	runDownStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
	runDetailStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	runRuleStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
}

// Lamp glyphs. Three shapes, not three colours of one shape: the pane is read at
// a glance and on a terminal with a narrow palette, and shape survives both.
//
// The same three everywhere the stack is shown -- the lamps, the Run tab, and
// the mark on the row that owns it -- and none of them the list's "●", which
// means one thing only: that session is waiting for you. Up was "●" and read as
// exactly that from the tab bar.
const (
	lampUpGlyph      = "▸"
	lampPendingGlyph = "▹"
	lampDownGlyph    = "✗"
)

// RunPane renders the singleton dev stack: its readiness lamps and the output of
// the pane it runs in.
//
// It holds no session of its own -- the stack does -- so unlike TerminalPane
// there is nothing here keyed by instance. That asymmetry is the design: one
// stack exists at a time, and this pane shows it wherever it is pointed.
type RunPane struct {
	mu            sync.Mutex
	width, height int
	stack         *dev.Stack
	content       string
	status        dev.Status
	// selected is the instance the cursor is on, which is not necessarily the
	// one the stack is running for -- saying so is most of this pane's job.
	selected string
	// selectedRepo names the repository that instance belongs to, for the empty
	// state: a stack is defined per repository, so "none configured" is only ever
	// true of a particular one and the message has to say which.
	selectedRepo string

	isScrolling bool
	viewport    viewport.Model
}

func NewRunPane(stack *dev.Stack) *RunPane {
	return &RunPane{stack: stack, viewport: viewport.New(0, 0)}
}

func (d *RunPane) SetSize(width, height int) {
	width, height = max(width, 0), max(height, 0)
	d.mu.Lock()
	d.width, d.height = width, height
	d.viewport.Width = width
	d.viewport.Height = height
	stack := d.stack
	d.mu.Unlock()

	if stack != nil {
		// Leave room for the lamp header, so the stack's own output wraps to the
		// width it is actually drawn at.
		stack.SetSize(width, max(height-headerLines(stack.CheckCount()), 0))
	}
}

// UpdateContent refreshes the pane for the selected instance.
func (d *RunPane) UpdateContent(instance *session.Instance) error {
	d.mu.Lock()
	stack := d.stack
	if instance != nil {
		d.selected = instance.Title
		d.selectedRepo = filepath.Base(filepath.Clean(instance.RepoPath()))
	} else {
		d.selected, d.selectedRepo = "", ""
	}
	d.mu.Unlock()

	if stack == nil {
		return nil
	}
	// A capture would scroll the view out from under the reader.
	d.mu.Lock()
	scrolling := d.isScrolling
	d.mu.Unlock()
	if scrolling {
		return nil
	}

	status := stack.Snapshot()
	content, err := stack.Capture()

	d.mu.Lock()
	d.status = status
	if err == nil {
		d.content = content
	}
	d.mu.Unlock()

	if err != nil {
		return fmt.Errorf("run pane: failed to capture stack output: %w", err)
	}
	return nil
}

func (d *RunPane) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.width == 0 || d.height == 0 {
		return strings.Repeat("\n", d.height)
	}
	if d.isScrolling {
		return d.viewport.View()
	}
	if d.stack == nil || !d.stack.Configured() {
		where := "this repository"
		if d.selectedRepo != "" {
			where = d.selectedRepo
		}
		return d.centred(fmt.Sprintf(
			"No dev stack configured for %s.\n\nAdd an entry under \"dev_by_repo\"\nin the Adroit config to run it from a session.", where))
	}
	if !d.status.Running {
		hint := "Press d to run the dev stack here."
		if d.selected == "" {
			hint = "Select a session, then press d to run the dev stack."
		}
		msg := "No dev stack is running.\n\n" + hint
		if d.status.Err != "" {
			msg = "The dev stack failed to start.\n\n" + wrap(d.status.Err, d.width-4)
		}
		return d.centred(msg)
	}

	header := d.header()
	// TrimRight for the same reason as the terminal pane: capture-pane's trailing
	// newline is not a row, and keeping it costs the oldest line on screen and
	// leaves a blank one under the newest.
	body := strings.Split(strings.TrimRight(d.content, "\n"), "\n")

	bodyHeight := d.height - headerLines(len(d.status.Lamps))
	if bodyHeight < 1 {
		return runPaneStyle.Width(d.width).Render(header)
	}
	if len(body) > bodyHeight {
		body = body[len(body)-bodyHeight:]
	} else {
		body = append(body, make([]string, bodyHeight-len(body))...)
	}
	for i, line := range body {
		body[i] = clip(line, d.width)
	}

	return runPaneStyle.Width(d.width).Render(header + "\n" + strings.Join(body, "\n"))
}

// segment is a run of text with a style, laid out by renderLine.
type segment struct {
	text  string
	style lipgloss.Style
}

// renderLine lays segments left to right, truncating at the pane's width.
//
// Truncation matters more than it looks: a header line long enough to wrap would
// make the header one row taller, and every row of the output below it would
// jump by one.
func renderLine(segs []segment, width int) string {
	var b strings.Builder
	used := 0
	for _, seg := range segs {
		if seg.text == "" {
			continue
		}
		room := width - used
		if room <= 0 {
			break
		}
		text := seg.text
		if runewidth.StringWidth(text) > room {
			text = runewidth.Truncate(text, room, "…")
		}
		b.WriteString(seg.style.Render(text))
		used += runewidth.StringWidth(text)
	}
	return b.String()
}

// headerLines is the height of the header: the title, one row per lamp, and the
// rule beneath them.
func headerLines(lampCount int) int {
	if lampCount < 1 {
		// The "no checks configured" line still occupies a row.
		lampCount = 1
	}
	return lampCount + 2
}

// header draws the stack's identity and its lamps.
//
// One lamp per row, in fixed columns, rather than a single row of them. The
// details a lamp reports change constantly while a stack comes up -- "waiting",
// "not listening", "no response", "up" -- and on a shared row every change in
// one of them shifts every lamp to its right. The icons end up moving on every
// poll, which is unreadable at exactly the moment you are trying to read them.
// Down a column nothing moves horizontally at all: the glyph and the name hold
// their positions for the life of the stack, and only the detail text changes.
func (d *RunPane) header() string {
	lines := []string{d.titleLine()}

	// One width for every name, so the details line up as a column too.
	nameWidth := 0
	for _, l := range d.status.Lamps {
		if w := runewidth.StringWidth(l.Name); w > nameWidth {
			nameWidth = w
		}
	}

	for _, l := range d.status.Lamps {
		glyph, style := lampUpGlyph, runUpStyle
		switch l.State {
		case dev.LampPending:
			glyph, style = lampPendingGlyph, runPendingStyle
		case dev.LampDown:
			glyph, style = lampDownGlyph, runDownStyle
		}
		lines = append(lines, renderLine([]segment{
			{"  ", runPaneStyle},
			{glyph, style},
			{" ", runPaneStyle},
			{padRight(l.Name, nameWidth), runPaneStyle},
			{"  ", runPaneStyle},
			{l.Detail, runDetailStyle},
		}, d.width))
	}

	if len(d.status.Lamps) == 0 {
		lines = append(lines, runDetailStyle.Render("  no checks configured"))
	}

	lines = append(lines, runRuleStyle.Render(strings.Repeat("─", max(0, d.width))))
	return strings.Join(lines, "\n")
}

// titleLine names the stack and what it is doing.
//
// The phase comes last because it is the part that changes: anything after it
// would be pushed around every time it did.
func (d *RunPane) titleLine() string {
	segs := []segment{{"stack › " + d.status.Title, runHeaderStyle}}

	if d.selected != "" && d.selected != d.status.Title {
		// The distinction the pane exists to make plain: the output below belongs
		// to another session, and d would move it here.
		segs = append(segs, segment{"  (not " + d.selected + " — press d to move it here)", runPhaseStyle})
	}

	phase := d.status.Phase
	if d.status.Err != "" {
		phase = d.status.Err
	}
	if phase != "" {
		segs = append(segs, segment{"  " + phase, runPhaseStyle})
	}
	return renderLine(segs, d.width)
}

// clip cuts a line to the pane's width, rather than leaving it to be wrapped.
//
// This is what keeps the lamps at the top. tmux hands back joined lines --
// capture-pane -J undoes the wrapping tmux itself did -- so a single log line
// can be far wider than the pane. Rendered into a fixed-width block those wrap
// again, and N lines become more than N rows: an eight-row body of long lines
// renders sixteen, the block overflows its box, and the header is pushed out of
// view. Which is exactly when a stack is starting and the lines are longest.
//
// Cutting keeps one row per line, so the header cannot move. The full text is
// still there in the pane itself, under shift-↑ or ↵.
func clip(line string, width int) string {
	if width <= 0 || ansi.PrintableRuneWidth(line) <= width {
		return line
	}
	// Truncating mid-line drops whatever reset code closed it, so the last
	// colour would bleed down the rest of the pane.
	return truncate.StringWithTail(line, uint(width), "…") + "\x1b[0m"
}

// padRight pads s to width in terminal cells.
func padRight(s string, width int) string {
	if pad := width - runewidth.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func (d *RunPane) centred(msg string) string {
	body := splash(d.width, msg)

	// 3 = tab bar height, 4 = window style frame; the same arithmetic the other
	// panes use to sit their fallback in the middle.
	available := d.height - 3 - 4
	pad := available - len(strings.Split(body, "\n"))
	var b strings.Builder
	if pad > 0 {
		b.WriteString(strings.Repeat("\n", pad/2))
	}
	b.WriteString(body)
	return runPaneStyle.Width(d.width).Align(lipgloss.Center).Render(b.String())
}

// wrap breaks s onto lines no longer than width, at spaces.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Attach opens the stack's pane full-screen, so its processes can be signalled
// or its shell used directly.
func (d *RunPane) Attach() (chan struct{}, error) {
	d.mu.Lock()
	stack := d.stack
	d.mu.Unlock()
	if stack == nil {
		return nil, fmt.Errorf("no dev stack to attach to")
	}
	return stack.Attach()
}

// enterScrollMode loads the stack pane's full history into the viewport.
// Caller must hold d.mu.
func (d *RunPane) enterScrollMode() error {
	if d.stack == nil {
		return nil
	}
	content, err := d.stack.CaptureHistory()
	if err != nil {
		return fmt.Errorf("run pane: failed to capture stack history: %w", err)
	}
	if content == "" {
		return nil
	}
	footer := runDetailStyle.Render("ESC to exit scroll mode")
	d.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, d.header(), content, footer))
	d.viewport.GotoBottom()
	d.isScrolling = true
	return nil
}

// ScrollUp enters scroll mode if needed, then scrolls up.
func (d *RunPane) ScrollUp() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isScrolling {
		return d.enterScrollMode()
	}
	d.viewport.LineUp(1)
	return nil
}

// ScrollDown enters scroll mode if needed, then scrolls down.
func (d *RunPane) ScrollDown() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isScrolling {
		return d.enterScrollMode()
	}
	d.viewport.LineDown(1)
	return nil
}

// ResetToNormalMode leaves scroll mode.
func (d *RunPane) ResetToNormalMode() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isScrolling {
		return
	}
	d.isScrolling = false
	d.viewport.SetContent("")
	d.viewport.GotoTop()
}

// IsScrolling reports whether the run pane is in scroll mode.
func (d *RunPane) IsScrolling() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isScrolling
}
