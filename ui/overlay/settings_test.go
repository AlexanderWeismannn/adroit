package overlay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/secrets"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

type memStore map[string]string

func (m memStore) Get(n string) (string, error) {
	if v, ok := m[n]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}
func (m memStore) Set(n, v string) error { m[n] = v; return nil }
func (m memStore) Delete(n string) error { delete(m, n); return nil }
func (m memStore) Backend() string       { return "test store" }

func press(s *Settings, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		s.HandleKeyPress(msg, nil)
	}
}

func typeText(s *Settings, text string) {
	for _, r := range text {
		s.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, nil)
	}
}

func newTestSettings(t *testing.T) (*Settings, memStore, *[]map[string]any) {
	t.Helper()
	var saves []map[string]any
	store := memStore{}
	cfg := &config.Config{DefaultProgram: "claude", BranchPrefix: "jane/"}
	s := NewSettings(cfg, store, "/work/api", func(c map[string]any) error {
		saves = append(saves, c)
		return nil
	})
	s.SetWidth(120)
	return s, store, &saves
}

// The point of the screen: add an OpenAI agent, give it a key, and have the key
// land in the store -- not in anything written to config.json, and not on screen.
func TestSettingsAddsAnAgentAndStoresItsKeyOutsideTheConfig(t *testing.T) {
	s, store, saves := newTestSettings(t)
	const key = "sk-proj-0123456789abcdefSECRET"

	press(s, "a", "down", "enter") // add -> codex
	require.Len(t, s.Config().Profiles, 2)
	codex := s.Config().Profiles[1]
	require.Equal(t, "codex", codex.Name)
	require.Equal(t, []string{"OPENAI_API_KEY"}, codex.Keys)

	press(s, "s")
	typeText(s, key)
	require.NotContains(t, s.Render(), key, "the key is echoed while it is typed")
	press(s, "enter")

	require.Equal(t, key, store[secrets.Key("codex", "OPENAI_API_KEY")])
	for _, change := range *saves {
		encoded, err := json.Marshal(change)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), key, "a key reached the config file")
	}
	view := s.Render()
	require.NotContains(t, view, key)
	require.Contains(t, view, secrets.Mask(key))
}

// Removing an agent is two presses, and takes its stored keys with it: a key
// left behind for a profile nobody can see is a secret nobody will clean up.
func TestSettingsRemovesAnAgentOnlyOnTheSecondPressAndForgetsItsKeys(t *testing.T) {
	s, store, _ := newTestSettings(t)
	press(s, "a", "down", "enter", "s")
	typeText(s, "sk-x-123456789")
	press(s, "enter")
	require.Len(t, s.Config().Profiles, 2)

	press(s, "x")
	require.Len(t, s.Config().Profiles, 2, "one press must only arm the removal")
	press(s, "x")
	require.Len(t, s.Config().Profiles, 1)
	_, err := store.Get(secrets.Key("codex", "OPENAI_API_KEY"))
	require.ErrorIs(t, err, secrets.ErrNotFound)
}

// A workspace's agent is saved under repos, keyed by the repository, so new
// sessions there start with it.
func TestSettingsSetsARepositorysAgent(t *testing.T) {
	s, _, saves := newTestSettings(t)
	press(s, "a", "down", "enter") // codex exists to be chosen
	press(s, "tab", "enter")       // Workspaces -> this repository
	press(s, "down", "down", "enter")
	press(s, "down", "down", "enter") // global default, claude, codex

	last := (*saves)[len(*saves)-1]
	repos, ok := last["repos"].(map[string]*config.RepoConfig)
	require.True(t, ok, "saved %#v", last)
	require.Equal(t, "codex", repos["/work/api"].Agent)
}

// A key check runs only for an agent that has one, and its result shows.
func TestSettingsKeyCheck(t *testing.T) {
	s, _, _ := newTestSettings(t)
	var ran string
	check := func(p config.Profile, command string) tea.Cmd {
		ran = p.Name + ": " + command
		return func() tea.Msg { return nil }
	}
	cmd, _ := s.HandleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}, check)
	require.NotNil(t, cmd)
	require.True(t, strings.HasPrefix(ran, "claude: claude -p"), ran)

	s.HandleCheck(SettingsCheckMsg{Profile: "claude", Detail: "invalid x-api-key"})
	require.Contains(t, s.Render(), "invalid x-api-key")
}
