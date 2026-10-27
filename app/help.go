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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpText interface {
	// toContent returns the help UI content.
	toContent() string
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

// generalHelpHeader sets the mark beside the title rather than above it.
//
// Stacked, the mark's seven rows would push the key list down by that much on a
// screen that is already the longest in the interface; alongside, it costs the
// three rows the title block used anyway. JoinHorizontal centres the shorter
// column against the taller one, so the wordmark sits on the mark's midline.
func generalHelpHeader() string {
	title := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Adroit"),
		"A terminal UI that manages multiple Claude Code",
		"(and other local agents) in separate workspaces.",
	)
	return lipgloss.JoinHorizontal(lipgloss.Center, logoStyle.Render(ui.LogoMarkSmall), "  ", title)
}

func (h helpTypeGeneral) toContent() string {
	rows := []string{generalHelpHeader(), ""}
	for i, section := range keys.HelpSections {
		if i > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, headerStyle.Render(section.Title))
		if section.Legend {
			// Built by ui alongside the glyphs and styles it documents, so the
			// legend cannot drift from the rows it describes.
			rows = append(rows, lipgloss.JoinVertical(lipgloss.Left, ui.BadgeLegend()...))
			continue
		}
		for _, line := range section.Lines {
			rows = append(rows, helpRow(line.Text()))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// helpRow is one key and what it does, the dash lined up in one column.
func helpRow(key, desc string) string {
	const column = 10
	pad := strings.Repeat(" ", max(column-lipgloss.Width(key), 0))
	return keyStyle.Render(key) + descStyle.Render(pad+"- "+desc)
}

func (h helpTypeInstanceStart) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Instance Created"),
		"",
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
	return content
}

func (h helpTypeInstanceAttach) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Attaching to Instance"),
		"",
		descStyle.Render("To detach from a session, press ")+keyStyle.Render("ctrl-q"),
	)
	return content
}

func (h helpTypeInstanceCheckout) toContent() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Checkout Instance"),
		"",
		"Changes will be committed locally, and the branch name copied to your clipboard (where one is available) for you to check out.",
		"",
		"Feel free to make changes to the branch and commit them. When resuming, the session will continue from where you left off.",
		"",
		headerStyle.Render("Commands:"),
		keyStyle.Render("c")+descStyle.Render(" - Checkout: commit changes locally and pause session"),
		keyStyle.Render("r")+descStyle.Render(" - Resume a paused session"),
	)
	return content
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
	logoStyle   lipgloss.Style
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

		content := helpType.toContent()

		text := overlay.NewTextOverlay(content)
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
	logoStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Info))
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Warning))
	descStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
}
