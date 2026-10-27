package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AlexanderWeismannn/adroit/log"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/charmbracelet/lipgloss"
)

var (
	highlightColor lipgloss.TerminalColor
	windowStyle    lipgloss.Style
	// The tab bar is one row -- the window's own top border, with the labels set
	// into it -- where it used to be three rows of boxes. tabRuleStyle draws the
	// border runs, the label styles the names.
	tabRuleStyle, tabLabelStyle, tabActiveLabelStyle lipgloss.Style
)

func init() { theme.OnChange(applyTabTheme) }

func applyTabTheme() {
	highlightColor = theme.Color(theme.Accent)
	tabRuleStyle = lipgloss.NewStyle().Foreground(highlightColor)
	tabLabelStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	tabActiveLabelStyle = lipgloss.NewStyle().
		Foreground(theme.Color(theme.Text)).
		Bold(true).
		Underline(true)
	// Neither the top nor the bottom border is lipgloss's: the tab bar is the
	// top and the status footer is the bottom, each a rule with words set into
	// it, so the window pays no rows for chrome that says nothing.
	windowStyle = lipgloss.NewStyle().
		BorderForeground(highlightColor).
		Border(lipgloss.RoundedBorder(), false, true, false, true)
	unfocusedRuleStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	unfocusedWindowStyle = windowStyle.BorderForeground(theme.Color(theme.Muted))
	footerLiveStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	footerPausedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Warning))
}

// footerLiveStyle and footerPausedStyle colour the footer's one word the way
// the list colours the same states: green is the live view, amber is a thing
// waiting on you.
var footerLiveStyle, footerPausedStyle lipgloss.Style

// unfocusedRuleStyle and unfocusedWindowStyle draw the border while the list
// owns the keyboard.
var unfocusedRuleStyle, unfocusedWindowStyle lipgloss.Style

// rule is the border style for the current focus.
func (w *TabbedWindow) rule() lipgloss.Style {
	if w.focused {
		return tabRuleStyle
	}
	return unfocusedRuleStyle
}

func (w *TabbedWindow) box() lipgloss.Style {
	if w.focused {
		return windowStyle
	}
	return unfocusedWindowStyle
}

// footerHeight is the rows the status footer takes: one, the bottom border.
const footerHeight = 1

const (
	PreviewTab int = iota
	DiffTab
	TerminalTab
	RunTab
)

type Tab struct {
	Name   string
	Render func(width int, height int) string
}

// TabbedWindow has tabs at the top of a pane which can be selected. The tabs
// take up one rune of height.
type TabbedWindow struct {
	tabs []string

	activeTab int
	height    int
	width     int

	preview  *PreviewPane
	diff     *DiffPane
	terminal *TerminalPane
	run      *RunPane
	instance *session.Instance

	// devStackTitle is the session the development stack runs for, and
	// devStackState its collapsed verdict. Pushed in on the app's metadata tick
	// rather than read from the stack here: Snapshot asks tmux whether its
	// session exists, which forks a process, and this renders every frame.
	devStackTitle string
	devStackState dev.LampState

	// paneRate is how often the live pane is sampled, per second, for the
	// footer.
	paneRate float64
	// focused is whether the window owns the keyboard.
	focused bool
}

// SetDevStack records which session the development stack is running for, and
// how it is doing, so the Run tab can say so from whichever tab you are on.
func (w *TabbedWindow) SetDevStack(title string, state dev.LampState) {
	w.devStackTitle, w.devStackState = title, state
}

// runTabLabel decorates the Run tab with the stack's state.
//
// On the tab rather than only in the list, because the question -- is the stack
// running, and is it running for THIS session -- is one you ask from whichever
// tab you happen to be on, and the list badge only marks the row that owns it.
func (w *TabbedWindow) runTabLabel(width int) string {
	const base = "Run"
	if w.devStackTitle == "" {
		return base
	}

	glyph, style := devStackIcon, pausedStyle // running, but for another session
	if w.instance != nil && w.instance.Title == w.devStackTitle {
		switch w.devStackState {
		case dev.LampUp:
			glyph, style = lampUpGlyph, runUpStyle
		case dev.LampDown:
			glyph, style = lampDownGlyph, runDownStyle
		default:
			glyph, style = lampPendingGlyph, runPendingStyle
		}
	}

	// A tab too narrow for the suffix would wrap it onto a second row and take
	// the whole bar with it, so the label gives way rather than the layout.
	if width < len(base)+2 {
		return base
	}
	return base + " " + style.Render(glyph)
}

func NewTabbedWindow(preview *PreviewPane, diff *DiffPane, terminal *TerminalPane, runPane *RunPane) *TabbedWindow {
	return &TabbedWindow{
		tabs: []string{
			"Preview",
			"Diff",
			"Terminal",
			"Run",
		},
		preview:  preview,
		diff:     diff,
		terminal: terminal,
		run:      runPane,
	}
}

func (w *TabbedWindow) SetInstance(instance *session.Instance) {
	w.instance = instance
}

// AdjustPreviewWidth adjusts the width of the preview pane to be 90% of the provided width.
func AdjustPreviewWidth(width int) int {
	return int(float64(width) * 0.9)
}

func (w *TabbedWindow) SetSize(width, height int) {
	// The window draws its own border around this width, so its footprint on
	// screen is width + frame. Below about twenty columns the 90% above is no
	// longer enough to pay for that, and the pane rendered wider than the budget
	// it was given -- pushing the composed frame past the edge of the terminal.
	w.width = width - windowStyle.GetHorizontalFrameSize()
	if w.width < 0 {
		w.width = 0
	}
	w.height = height

	// Calculate the content height by subtracting:
	// 1. Tab height (including border and padding)
	// 2. Window style vertical frame size
	// 3. Additional padding/spacing (2 for the newline and spacing)
	//
	// A terminal that is still opening can report fewer rows than that chrome
	// takes, and a negative budget reached the panes: the preview sliced with it
	// and panicked on the first frame. Nothing is left to draw, so draw nothing.
	contentHeight := max(height-tabBarHeight-footerHeight-windowStyle.GetVerticalFrameSize(), 0)
	contentWidth := max(w.width-windowStyle.GetHorizontalFrameSize(), 0)

	w.preview.SetSize(contentWidth, contentHeight)
	w.diff.SetSize(contentWidth, contentHeight)
	w.terminal.SetSize(contentWidth, contentHeight)
	w.run.SetSize(contentWidth, contentHeight)
}

func (w *TabbedWindow) GetPreviewSize() (width, height int) {
	return w.preview.width, w.preview.height
}

func (w *TabbedWindow) Toggle() {
	w.activeTab = (w.activeTab + 1) % len(w.tabs)
}

// Prev selects the tab to the left, wrapping round.
func (w *TabbedWindow) Prev() {
	w.activeTab = (w.activeTab + len(w.tabs) - 1) % len(w.tabs)
}

// SetFocused records whether the window owns the keyboard, which its border
// shows: the accent when it does, the muted rule when the list does.
func (w *TabbedWindow) SetFocused(focused bool) {
	w.focused = focused
}

// UpdatePreview updates the content of the preview pane. instance may be nil.
func (w *TabbedWindow) UpdatePreview(instance *session.Instance) error {
	if w.activeTab != PreviewTab {
		return nil
	}
	return w.preview.UpdateContent(instance)
}

// ApplyPreviewCapture hands the preview pane a capture taken off the event loop.
// A no-op unless the preview tab is showing, which is the only state the tick
// captures in.
func (w *TabbedWindow) ApplyPreviewCapture(instance *session.Instance, content string) {
	if w.activeTab != PreviewTab {
		return
	}
	w.preview.ApplyCapture(instance, content)
}

func (w *TabbedWindow) UpdateDiff(instance *session.Instance) {
	if w.activeTab != DiffTab {
		return
	}
	w.diff.SetDiff(instance)
}

// UpdateRun updates the run pane content. Only updates when the run tab is active.
func (w *TabbedWindow) UpdateRun(instance *session.Instance) error {
	if w.activeTab != RunTab {
		return nil
	}
	return w.run.UpdateContent(instance)
}

// SampleTerminal and SampleRun capture the pane a tab shows off the event loop;
// ApplyTerminalSample and ApplyRunSample hand the result back on it. Each is a
// no-op unless that tab is still the one showing, which is the only state the
// tick samples in and the state it may no longer be in by the time the result
// arrives.
func (w *TabbedWindow) SampleTerminal(instance *session.Instance) (string, bool, error) {
	return w.terminal.Sample(instance)
}

func (w *TabbedWindow) ApplyTerminalSample(instance *session.Instance, content string) {
	if w.activeTab != TerminalTab {
		return
	}
	w.terminal.ApplySample(instance, content)
}

func (w *TabbedWindow) SampleRun() (string, dev.Status, bool, error) {
	return w.run.Sample()
}

func (w *TabbedWindow) ApplyRunSample(content string, status dev.Status) {
	if w.activeTab != RunTab {
		return
	}
	w.run.ApplySample(content, status)
}

// SelectRunTab makes the run tab active.
func (w *TabbedWindow) SelectRunTab() {
	w.activeTab = RunTab
}

// IsInRunTab reports whether the run tab is currently active.
func (w *TabbedWindow) IsInRunTab() bool {
	return w.activeTab == RunTab
}

// UpdateTerminal updates the terminal pane content. Only updates when terminal tab is active.
func (w *TabbedWindow) UpdateTerminal(instance *session.Instance) error {
	if w.activeTab != TerminalTab {
		return nil
	}
	return w.terminal.UpdateContent(instance)
}

// ResetPreviewToNormalMode resets the preview pane to normal mode
func (w *TabbedWindow) ResetPreviewToNormalMode(instance *session.Instance) error {
	return w.preview.ResetToNormalMode(instance)
}

// Add these new methods for handling scroll events
// PageUp and PageDown scroll the active pane by most of a screen. Shift+arrows
// moved one line a keypress, which made reading back through a long turn a
// matter of holding a key down.
func (w *TabbedWindow) PageUp() {
	for i := 0; i < w.pageLines(); i++ {
		w.ScrollUp()
	}
}

func (w *TabbedWindow) PageDown() {
	for i := 0; i < w.pageLines(); i++ {
		w.ScrollDown()
	}
}

// pageLines keeps two lines of the old page on screen, for context.
func (w *TabbedWindow) pageLines() int {
	return max(1, w.preview.height-2)
}

func (w *TabbedWindow) ScrollUp() {
	switch w.activeTab {
	case PreviewTab:
		err := w.preview.ScrollUp(w.instance)
		if err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll up: %v", err)
		}
	case DiffTab:
		w.diff.ScrollUp()
	case RunTab:
		if err := w.run.ScrollUp(); err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll run pane up: %v", err)
		}
	case TerminalTab:
		if err := w.terminal.ScrollUp(); err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll terminal up: %v", err)
		}
	}
}

func (w *TabbedWindow) ScrollDown() {
	switch w.activeTab {
	case PreviewTab:
		err := w.preview.ScrollDown(w.instance)
		if err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll down: %v", err)
		}
	case DiffTab:
		w.diff.ScrollDown()
	case RunTab:
		if err := w.run.ScrollDown(); err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll run pane down: %v", err)
		}
	case TerminalTab:
		if err := w.terminal.ScrollDown(); err != nil {
			log.InfoLog.Printf("tabbed window failed to scroll terminal down: %v", err)
		}
	}
}

// IsInPreviewTab returns true if the preview tab is currently active
func (w *TabbedWindow) IsInPreviewTab() bool {
	return w.activeTab == PreviewTab
}

// IsInDiffTab returns true if the diff tab is currently active
func (w *TabbedWindow) IsInDiffTab() bool {
	return w.activeTab == DiffTab
}

// IsInTerminalTab returns true if the terminal tab is currently active
func (w *TabbedWindow) IsInTerminalTab() bool {
	return w.activeTab == TerminalTab
}

// GetActiveTab returns the currently active tab index
func (w *TabbedWindow) GetActiveTab() int {
	return w.activeTab
}

// AttachRun attaches to the dev stack's tmux session.
func (w *TabbedWindow) AttachRun() (chan struct{}, error) {
	return w.run.Attach()
}

// IsRunInScrollMode reports whether the run pane is in scroll mode.
func (w *TabbedWindow) IsRunInScrollMode() bool {
	return w.run.IsScrolling()
}

// ResetRunToNormalMode exits scroll mode on the run pane.
func (w *TabbedWindow) ResetRunToNormalMode() {
	w.run.ResetToNormalMode()
}

// AttachTerminal attaches to the terminal tmux session
func (w *TabbedWindow) AttachTerminal() (chan struct{}, error) {
	return w.terminal.Attach()
}

// CleanupTerminal closes the terminal session
func (w *TabbedWindow) CleanupTerminal() {
	w.terminal.Close()
}

// CleanupTerminalForInstance closes the cached terminal session for the given instance title.
func (w *TabbedWindow) CleanupTerminalForInstance(title string) {
	w.terminal.CloseForInstance(title)
}

// IsPreviewInScrollMode returns true if the preview pane is in scroll mode
func (w *TabbedWindow) IsPreviewInScrollMode() bool {
	return w.preview.isScrolling
}

// IsTerminalInScrollMode returns true if the terminal pane is in scroll mode
func (w *TabbedWindow) IsTerminalInScrollMode() bool {
	return w.terminal.IsScrolling()
}

// ResetTerminalToNormalMode exits scroll mode on the terminal pane
func (w *TabbedWindow) ResetTerminalToNormalMode() {
	w.terminal.ResetToNormalMode()
}

// tabBarHeight is the rows the tab bar takes: one.
const tabBarHeight = 1

// tabBar draws the tabs as the window's top border, exactly width cells wide:
//
//	╭─ Preview · Diff · Terminal · Run ───────────────────╮
//
// Names only: what a tab would tell you -- the diff's size, whether the stack
// is up, whether you are scrolled -- is said in the footer below, and in the
// status column where there is one. The active tab is underlined. A bar too
// narrow for every name shortens them, and never wraps: a wrapped bar is a
// second row that pushes the whole pane down.
func (w *TabbedWindow) tabBar(width int) string {
	if width < 4 {
		return ""
	}
	render := func(nameWidth int) (string, int) {
		var b strings.Builder
		used := 0
		b.WriteString(w.rule().Render("╭─"))
		used += 2
		for i, name := range w.tabs {
			if i > 0 {
				b.WriteString(tabLabelStyle.Render(" · "))
				used += 3
			} else {
				b.WriteString(" ")
				used++
			}
			label := name
			if nameWidth > 0 && lipgloss.Width(label) > nameWidth {
				label = clip(label, nameWidth)
			}
			style := tabLabelStyle
			if i == w.activeTab {
				style = tabActiveLabelStyle
			}
			b.WriteString(style.Render(label))
			used += lipgloss.Width(label)
		}
		b.WriteString(" ")
		used++
		return b.String(), used
	}
	bar, used := render(0)
	for nameWidth := 8; used+2 > width && nameWidth >= 1; nameWidth-- {
		bar, used = render(nameWidth)
	}
	fill := width - used - 1
	if fill < 0 {
		return clip(bar, width)
	}
	return bar + w.rule().Render(strings.Repeat("─", fill)+"╮")
}

// diffSummary is the diff's size, as the footer says it: "+25k −2.4k".
func (w *TabbedWindow) diffSummary() string {
	if w.instance == nil {
		return ""
	}
	st := w.instance.GetDiffStats()
	if st == nil || st.Error != nil || st.IsEmpty() {
		return ""
	}
	var parts []string
	if st.Added > 0 {
		parts = append(parts, addedLinesStyle.Render("+"+compactCount(st.Added)))
	}
	if st.Removed > 0 {
		parts = append(parts, removedLinesStyle.Render("−"+compactCount(st.Removed)))
	}
	return strings.Join(parts, " ")
}

// SetPaneRate records how often the live pane is being sampled, which the
// footer shows beside "live": the number says the pane is moving.
func (w *TabbedWindow) SetPaneRate(perSecond float64) {
	w.paneRate = perSecond
}

// paneStatus is the footer's one phrase: whether what the pane shows is the
// live view, and if not, what it is instead and the key that brings the live
// view back.
//
// A live pane and a stale one looked identical. A session parked by checkout,
// a Terminal tab scrolled back into its history, a Run tab with the stack down
// -- each was a still pane under the same border as a live one, and the only
// way to tell was to wait and see whether anything moved.
func (w *TabbedWindow) paneStatus() (text string, style lipgloss.Style) {
	live := func() (string, lipgloss.Style) {
		if w.paneRate > 0 {
			return fmt.Sprintf("● live · %g/s", w.paneRate), footerLiveStyle
		}
		return "● live", footerLiveStyle
	}
	switch w.activeTab {
	case PreviewTab:
		// The preview pins its own status line under the scrollback while it is
		// scrolled, with the position in it; the footer does not say it twice.
		if w.instance == nil || w.preview.isScrolling || !w.instance.Started() {
			return "", tabLabelStyle
		}
		if w.instance.Status == session.Paused {
			return "⏸ paused · r resumes", footerPausedStyle
		}
		return live()
	case DiffTab:
		summary := w.diffSummary()
		if w.diff.IsScrolling() {
			return strings.TrimSpace("↕ scrolled · " + summary), tabLabelStyle
		}
		return summary, tabLabelStyle
	case TerminalTab:
		if w.terminal.IsScrolling() {
			return "↕ scrolled", tabLabelStyle
		}
		if w.instance != nil && w.instance.Started() && w.instance.Status != session.Paused {
			return live()
		}
	case RunTab:
		if w.run.IsScrolling() {
			return "↕ scrolled", tabLabelStyle
		}
		if w.run.running() {
			return live()
		}
		if label := w.runTabLabel(len("Run") + 2); label != "Run" {
			// The stack is up for another session: say so, with its mark.
			return strings.TrimSpace(strings.TrimPrefix(label, "Run")) + " " + w.devStackTitle, tabLabelStyle
		}
	}
	return "", tabLabelStyle
}

// footer draws the window's bottom border with the pane's status set into it,
// exactly width cells wide:
//
//	╰─ ● live ──────────────────────────────────────────╯
//
// A status too long for the width is dropped rather than wrapped, for the same
// reason the tab bar drops its labels: a second row takes the layout with it.
func (w *TabbedWindow) footer(width int) string {
	if width < 2 {
		return ""
	}
	text, style := w.paneStatus()
	label := ""
	if text != "" {
		label = " " + style.Render(text) + " "
	}
	// The corners and the one rule cell before the label: "╰─ ● live ──╯".
	fill := width - 3 - lipgloss.Width(label)
	if fill < 0 {
		label, fill = "", width-3
	}
	if fill < 0 {
		return clip(w.rule().Render("╰╯"), width)
	}
	return w.rule().Render("╰─") + label + w.rule().Render(strings.Repeat("─", fill)+"╯")
}

// compactCount shortens a line count for a badge: 980, 2.4k, 25k.
func compactCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 9950: // past this the one decimal would round up to "10.0k"
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	default:
		return strconv.Itoa((n+500)/1000) + "k"
	}
}

func (w *TabbedWindow) String() string {
	if w.width == 0 || w.height == 0 {
		return ""
	}

	row := w.tabBar(w.width + windowStyle.GetHorizontalFrameSize())
	var content string
	switch w.activeTab {
	case PreviewTab:
		content = w.preview.String()
	case DiffTab:
		content = w.diff.String()
	case TerminalTab:
		content = w.terminal.String()
	case RunTab:
		content = w.run.String()
	}
	window := w.box().Render(
		lipgloss.Place(
			w.width, w.height-windowStyle.GetVerticalFrameSize()-tabBarHeight-footerHeight,
			lipgloss.Left, lipgloss.Top, content))
	footer := w.footer(w.width + windowStyle.GetHorizontalFrameSize())

	composed := lipgloss.JoinVertical(lipgloss.Left, row, window, footer)

	// The window is a fixed-size pane, so it must occupy exactly the rows it was
	// given. A pane whose content does not fit -- the splash in a short terminal
	// is the one that does it -- would otherwise push the composed view past the
	// bottom of the screen, and the whole thing scrolls by a row on every redraw.
	if lines := strings.Split(composed, "\n"); w.height > 0 && len(lines) > w.height {
		composed = strings.Join(lines[:w.height], "\n")
	}
	return composed
}
