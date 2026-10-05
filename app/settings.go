package app

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/secrets"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"

	tea "github.com/charmbracelet/bubbletea"
)

// checkTimeout bounds a key check: the agent's first reply over the network.
const checkTimeout = 90 * time.Second

func (m *home) openSettings() (tea.Model, tea.Cmd) {
	repo := ""
	if cwd, err := os.Getwd(); err == nil {
		if root, err := git.RepoRoot(cwd); err == nil {
			repo = root
		}
	}
	m.settings = overlay.NewSettings(m.appConfig, secrets.Default(), repo, m.saveSettings)
	m.settings.SetWidth(m.termWidth)
	m.state = stateSettings
	return m, nil
}

// saveSettings writes the keys the settings screen changed, keeping anything
// else in the file, and adopts the result so the change applies at once.
func (m *home) saveSettings(changes map[string]any) error {
	if err := config.UpdateConfigFile(changes); err != nil {
		return err
	}
	m.appConfig = config.LoadConfig()
	if !m.programOverridden {
		m.program = m.appConfig.GetProgram()
	}
	return nil
}

func (m *home) handleSettingsState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.closeSettings()
		return m, nil
	}
	cmd, closed := m.settings.HandleKeyPress(msg, checkAgentKey)
	if closed {
		m.closeSettings()
	}
	return m, cmd
}

func (m *home) closeSettings() {
	m.settings = nil
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)
}

// checkAgentKey runs the agent once, through agent-run so it gets exactly the
// keys a session would, and reports whether it answered.
func checkAgentKey(profile config.Profile, command string) tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			return overlay.SettingsCheckMsg{Profile: profile.Name, Detail: err.Error()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "agent-run", "--profile", profile.Name, "--", command)
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return overlay.SettingsCheckMsg{Profile: profile.Name, Detail: "no answer in 90s"}
		}
		if err != nil {
			return overlay.SettingsCheckMsg{Profile: profile.Name, Detail: lastLine(string(out), err.Error())}
		}
		return overlay.SettingsCheckMsg{Profile: profile.Name, OK: true}
	}
}

func lastLine(out, fallback string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return fallback
}

// newSessionProgram is what a new session runs: -p when given, else the
// repository's own agent, else the default.
func (m *home) newSessionProgram() string {
	if m.programOverridden {
		return m.program
	}
	if cwd, err := os.Getwd(); err == nil {
		if root, err := git.RepoRoot(cwd); err == nil {
			return m.appConfig.ProgramFor(root)
		}
	}
	return m.program
}
