package ui

import (
	"github.com/AlexanderWeismannn/adroit/log"
	"strconv"
	"strings"

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
		Background(highlightColor).
		Foreground(theme.Color(theme.AccentText)).
		Bold(true)
	windowStyle = lipgloss.NewStyle().
		BorderForeground(highlightColor).
		Border(lipgloss.RoundedBorder(), false, true, true, true)
}

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
	contentHeight := max(height-tabBarHeight-windowStyle.GetVerticalFrameSize()-1, 0)
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

// tabLabel is one tab's name and, where it has one, what it would tell you if
// you switched to it -- so the bar answers "is there a diff, is the stack up, am
// I scrolled" from whichever tab you are on.
func (w *TabbedWindow) tabLabel(tab int) (name, status string) {
	switch tab {
	case PreviewTab:
		name = "Preview"
		if w.preview.isScrolling {
			status = "⇡ scrolled"
		}
	case DiffTab:
		name = "Diff"
		if w.instance != nil {
			if st := w.instance.GetDiffStats(); st != nil && st.Error == nil && !st.IsEmpty() {
				var parts []string
				if st.Added > 0 {
					parts = append(parts, addedLinesStyle.Render("+"+compactCount(st.Added)))
				}
				if st.Removed > 0 {
					parts = append(parts, removedLinesStyle.Render("−"+compactCount(st.Removed)))
				}
				status = strings.Join(parts, " ")
			}
		}
		if w.diff.IsScrolling() {
			status = strings.TrimSpace(status + " ⇡")
		}
	case TerminalTab:
		name = "Terminal"
		if w.terminal.IsScrolling() {
			status = "⇡ scrolled"
		}
	case RunTab:
		name = "Run"
		if label := w.runTabLabel(len(name) + 2); label != name {
			status = strings.TrimSpace(strings.TrimPrefix(label, name))
		}
		if w.run.IsScrolling() {
			status = strings.TrimSpace(status + " ⇡")
		}
	}
	return name, status
}

// tabBar draws the tabs as the window's top border, exactly width cells wide:
//
//	╭─ Preview ─┬─ Diff +25k −2.4k ─┬─ Terminal ─┬─ Run ● ──────────╮
//
// The active tab is reverse video in the accent. A bar too narrow for every
// label drops the statuses first and then shortens the names, and never wraps:
// a wrapped bar is a second row that pushes the whole pane down.
func (w *TabbedWindow) tabBar(width int) string {
	if width < 4 {
		return ""
	}
	type tab struct{ name, status string }
	tabs := make([]tab, len(w.tabs))
	for i := range w.tabs {
		n, st := w.tabLabel(i)
		tabs[i] = tab{n, st}
	}
	render := func(withStatus bool, nameWidth int) (string, int) {
		var b strings.Builder
		used := 0
		b.WriteString(tabRuleStyle.Render("╭─"))
		used += 2
		for i, t := range tabs {
			if i > 0 {
				b.WriteString(tabRuleStyle.Render("─┬─"))
				used += 3
			}
			label := t.name
			if nameWidth > 0 && lipgloss.Width(label) > nameWidth {
				label = clip(label, nameWidth)
			}
			text := " " + label + " "
			style := tabLabelStyle
			if i == w.activeTab {
				style = tabActiveLabelStyle
			}
			b.WriteString(style.Render(text))
			used += lipgloss.Width(text)
			if withStatus && t.status != "" {
				b.WriteString(t.status + " ")
				used += lipgloss.Width(t.status) + 1
			}
		}
		return b.String(), used
	}
	bar, used := render(true, 0)
	if used+2 > width {
		bar, used = render(false, 0)
	}
	for nameWidth := 8; used+2 > width && nameWidth >= 1; nameWidth-- {
		bar, used = render(false, nameWidth)
	}
	fill := width - used - 1
	if fill < 0 {
		return clip(bar, width)
	}
	return bar + tabRuleStyle.Render(strings.Repeat("─", fill)+"╮")
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
	window := windowStyle.Render(
		lipgloss.Place(
			w.width, w.height-1-windowStyle.GetVerticalFrameSize()-tabBarHeight,
			lipgloss.Left, lipgloss.Top, content))

	// One blank row above the tabs, matching the single row the list leads with,
	// so the two panes start level. A bare "\n" is two rows, not one.
	composed := lipgloss.JoinVertical(lipgloss.Left, "", row, window)

	// The window is a fixed-size pane, so it must occupy exactly the rows it was
	// given. A pane whose content does not fit -- the splash in a short terminal
	// is the one that does it -- would otherwise push the composed view past the
	// bottom of the screen, and the whole thing scrolls by a row on every redraw.
	if lines := strings.Split(composed, "\n"); w.height > 0 && len(lines) > w.height {
		composed = strings.Join(lines[:w.height], "\n")
	}
	return composed
}
