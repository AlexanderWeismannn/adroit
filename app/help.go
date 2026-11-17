package app

import (
	"fmt"
	"strings"

	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type helpText interface {
	// title is the card's name, in the frame's border.
	title() string
	// toContent returns the card laid out for width columns: long lines
	// wrapped, and the general card in two columns where there is room.
	toContent(width int) string
	// mask returns the bit mask for this help text. These are used to track which help screens
	// have been seen in the config and app state.
	mask() uint32
}

type helpTypeGeneral struct{}

type helpTypeInstanceStart struct {
	instance *session.Instance
}

type helpTypeInstanceAttach struct{}

type helpTypeInstanceCheckout struct{}

func helpStart(instance *session.Instance) helpText {
	return helpTypeInstanceStart{instance: instance}
}

// helpIntro is the card's first line. The braille mark used to sit beside it,
// seven rows tall on the screen that is already the longest in the interface,
// and it pushed the last sections and the keys line off every terminal size.
func helpIntro(width int) string {
	return ansi.Wrap(titleStyle.Render("Adroit")+descStyle.Render(" runs Claude Code and other agents, each in its own workspace."), width, "")
}

// helpTwoColumnsFrom is the narrowest card that splits into two columns: each
// then keeps room for the key column and a description that rarely wraps.
const helpTwoColumnsFrom = 110

// helpSize is the card's frame for a terminal: wide enough for one column of
// keys, or two from 120 columns on, and never taller than the screen less the
// header and bar rows -- the card scrolls inside that rather than being cut.
func helpSize(termWidth, termHeight int) (width, height int) {
	width = min(max(termWidth-4, 20), 90)
	if termWidth >= 120 {
		width = min(termWidth-4, 150)
	}
	return width, max(termHeight-2, 8)
}

func (h helpTypeGeneral) toContent(width int) string {
	section := func(s keys.HelpSection, width int) []string {
		rows := []string{headerStyle.Render(s.Title)}
		if s.Legend {
			// Built by ui alongside the glyphs and styles it documents, so the
			// legend cannot drift from the rows it describes.
			return append(rows, ui.BadgeLegend(width)...)
		}
		for _, line := range s.Lines {
			key, desc := line.Text()
			rows = append(rows, helpRow(key, desc, width))
		}
		return rows
	}
	column := func(sections []keys.HelpSection, width int) string {
		var rows []string
		for i, s := range sections {
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, section(s, width)...)
		}
		return lipgloss.JoinVertical(lipgloss.Left, rows...)
	}

	intro := helpIntro(width)
	if width < helpTwoColumnsFrom {
		return lipgloss.JoinVertical(lipgloss.Left, intro, "", column(keys.HelpSections, width))
	}
	// Two columns: the keys that act on sessions on the left, the legend and
	// the interface's own keys on the right.
	const gutter = 4
	half := (width - gutter) / 2
	var left, right []keys.HelpSection
	for _, s := range keys.HelpSections {
		if s.Legend || len(right) > 0 {
			right = append(right, s)
		} else {
			left = append(left, s)
		}
	}
	l := lipgloss.NewStyle().Width(half).Render(column(left, half))
	return lipgloss.JoinVertical(lipgloss.Left, intro, "",
		lipgloss.JoinHorizontal(lipgloss.Top, l, strings.Repeat(" ", gutter), column(right, half)))
}

// helpKeyColumn is the width of the key column, dash included.
const helpKeyColumn = 12

// helpRow is one key and what it does, the dash lined up in one column and a
// long description wrapped under itself rather than cut at the border.
func helpRow(key, desc string, width int) string {
	pad := strings.Repeat(" ", max(helpKeyColumn-2-lipgloss.Width(key), 1))
	lines := strings.Split(ansi.Wrap(desc, max(width-helpKeyColumn, 10), ""), "\n")
	out := keyStyle.Render(key) + descStyle.Render(pad+"- "+lines[0])
	for _, line := range lines[1:] {
		out += "\n" + descStyle.Render(strings.Repeat(" ", helpKeyColumn)+line)
	}
	return out
}

func (h helpTypeGeneral) title() string          { return "Keys" }
func (h helpTypeInstanceStart) title() string    { return "Session created" }
func (h helpTypeInstanceAttach) title() string   { return "Attaching" }
func (h helpTypeInstanceCheckout) title() string { return "Checkout" }

func (h helpTypeInstanceStart) toContent(width int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		descStyle.Render("New session created:"),
		descStyle.Render(fmt.Sprintf("• Git branch: %s (isolated worktree)",
			lipgloss.NewStyle().Bold(true).Render(h.instance.Branch))),
		descStyle.Render(fmt.Sprintf("• %s running in background tmux session",
			lipgloss.NewStyle().Bold(true).Render(h.instance.Program))),
		"",
		headerStyle.Render("Managing:"),
		keyStyle.Render("↵/o")+descStyle.Render("   - Attach to the session to interact with it directly"),
		keyStyle.Render("tab")+descStyle.Render("   - Switch preview panes to view session diff"),
		keyStyle.Render("D")+descStyle.Render("     - Kill (delete) the selected session"),
		"",
		headerStyle.Render("Handoff:"),
		keyStyle.Render("c")+descStyle.Render("     - Checkout this instance's branch"),
		keyStyle.Render("p")+descStyle.Render("     - Push branch to GitHub to create a PR"),
		keyStyle.Render("g")+descStyle.Render("     - Open that PR in your browser once it exists"),
	)
	return ansi.Wrap(content, width, "")
}

func (h helpTypeInstanceAttach) toContent(width int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		descStyle.Render("To detach from a session, press ")+keyStyle.Render("ctrl-q"),
	)
	return ansi.Wrap(content, width, "")
}

func (h helpTypeInstanceCheckout) toContent(width int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		"Changes will be committed locally, and the branch name copied to your clipboard (where one is available) for you to check out.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		keyStyle.Render("c")+descStyle.Render(" - Checkout: commit changes locally and pause session"),
		keyStyle.Render("r")+descStyle.Render(" - Resume a paused session"),
	)
	return ansi.Wrap(content, width, "")
}
func (h helpTypeGeneral) mask() uint32 {
	return 1
}

func (h helpTypeInstanceStart) mask() uint32 {
	return 1 << 1
}
func (h helpTypeInstanceAttach) mask() uint32 {
	return 1 << 2
}
func (h helpTypeInstanceCheckout) mask() uint32 {
	return 1 << 3
}

var (
	titleStyle  lipgloss.Style
	headerStyle lipgloss.Style
	keyStyle    lipgloss.Style
	descStyle   lipgloss.Style
)

// showHelpScreen displays the help screen overlay if it hasn't been shown before
func (m *home) showHelpScreen(helpType helpText, onDismiss func() tea.Cmd) (tea.Model, tea.Cmd) {
	// Get the flag for this help type
	var alwaysShow bool
	switch helpType.(type) {
	case helpTypeGeneral:
		alwaysShow = true
	}

	flag := helpType.mask()

	// Check if this help screen has been seen before
	// Only show if we're showing the general help screen or the corresponding flag is not set
	// in the seen bitmask.
	if alwaysShow || (m.appState.GetHelpScreensSeen()&flag) == 0 {
		// Mark this help screen as seen and save state
		if err := m.appState.SetHelpScreensSeen(m.appState.GetHelpScreensSeen() | flag); err != nil {
			log.WarningLog.Printf("Failed to save help screen state: %v", err)
		}

		text := overlay.NewTextOverlayLayout(helpType.toContent)
		text.Title = helpType.title()
		// Sized now rather than on the next resize: until one arrives the card
		// would be drawn at its natural width and cut by a narrow terminal.
		if _, general := helpType.(helpTypeGeneral); !general {
			// The one-off cards are a few lines of prose: a line as long as the
			// general card's two columns is hard to read.
			text.MaxWidth = 72
		}
		text.SetSize(helpSize(m.termWidth, m.termHeight))
		text.OnDismiss = onDismiss
		m.overlay = &helpScreen{text: text}
		return m, nil
	}

	// Skip displaying the help screen
	if onDismiss != nil {
		return m, onDismiss()
	}
	return m, nil
}

func init() { theme.OnChange(applyHelpTheme) }

func applyHelpTheme() {
	titleStyle = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(theme.Color(theme.Accent))
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Info))
	// Keys in the accent, as on the bar and every overlay's keys line. They
	// were Warning, which several light palettes draw as pale yellow on white.
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent))
	descStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
}
