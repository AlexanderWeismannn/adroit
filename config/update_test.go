package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func configPathIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".adroit"), 0755))
	return filepath.Join(home, ".adroit", ConfigFileName)
}

// The whole reason this does not go through saveConfig: marshalling Config drops
// every key the struct does not know about. Nothing rewrote an existing config
// before, so that loss had never been possible, and a command that edits one
// setting must not introduce it.
func TestUpdateConfigFilePreservesUnknownKeys(t *testing.T) {
	path := configPathIn(t)
	require.NoError(t, os.WriteFile(path, []byte(`{
		"default_program": "claude",
		"branch_prefix": "TASK-",
		"a_key_from_a_newer_version": {"nested": [1, 2]},
		"hand_added": "keep me"
	}`), 0644))

	require.NoError(t, UpdateConfigFile(map[string]any{"theme": "dracula"}))

	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &raw))

	require.JSONEq(t, `"dracula"`, string(raw["theme"]))
	require.JSONEq(t, `"claude"`, string(raw["default_program"]))
	require.JSONEq(t, `"TASK-"`, string(raw["branch_prefix"]))
	require.JSONEq(t, `{"nested":[1,2]}`, string(raw["a_key_from_a_newer_version"]))
	require.JSONEq(t, `"keep me"`, string(raw["hand_added"]))
}

// A nil value removes the key, so a reset returns the file to not mentioning
// colour at all rather than pinning "default" forever.
func TestUpdateConfigFileDeletesOnNil(t *testing.T) {
	path := configPathIn(t)
	require.NoError(t, os.WriteFile(path, []byte(`{"theme":"nord","colors":{"accent":"#fff"},"keep":"yes"}`), 0644))

	require.NoError(t, UpdateConfigFile(map[string]any{"theme": nil, "colors": nil}))

	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &raw))

	_, hasTheme := raw["theme"]
	_, hasColors := raw["colors"]
	require.False(t, hasTheme)
	require.False(t, hasColors)
	require.JSONEq(t, `"yes"`, string(raw["keep"]))
}

// A file we cannot parse is the user's, and it is the one case where rewriting
// destroys the very thing that needs hand-fixing.
func TestUpdateConfigFileRefusesToOverwriteUnparseableJSON(t *testing.T) {
	path := configPathIn(t)
	broken := []byte(`{"theme": "nord",,,}`)
	require.NoError(t, os.WriteFile(path, broken, 0644))

	err := UpdateConfigFile(map[string]any{"theme": "dracula"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not valid JSON")

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, broken, after, "the unreadable file must be left exactly as it was")
}

// A file created here should look like one created by a first run, not hold a
// lone "theme" key.
func TestUpdateConfigFileSeedsFromDefaultsWhenMissing(t *testing.T) {
	path := configPathIn(t)
	require.NoError(t, UpdateConfigFile(map[string]any{"theme": "gruvbox"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	raw := map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(data, &raw))

	require.JSONEq(t, `"gruvbox"`, string(raw["theme"]))
	require.Contains(t, raw, "default_program")
	require.Contains(t, raw, "daemon_poll_interval")
}

// The write goes through a temporary file so an interrupted one cannot leave a
// truncated config; that file must not survive a successful write.
func TestUpdateConfigFileLeavesNoTemporaryFile(t *testing.T) {
	path := configPathIn(t)
	require.NoError(t, UpdateConfigFile(map[string]any{"theme": "nord"}))

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.Contains(entry.Name(), ".tmp"), "left behind %s", entry.Name())
	}
}

func TestReadConfigKey(t *testing.T) {
	path := configPathIn(t)

	// Absent file: not an error, just not there.
	_, ok, err := ReadConfigKey("colors")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, os.WriteFile(path, []byte(`{"colors":{"accent":{"light":"#111","dark":"#222"}}}`), 0644))
	raw, ok, err := ReadConfigKey("colors")
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `{"accent":{"light":"#111","dark":"#222"}}`, string(raw))

	// Present file, absent key: distinguishable from a key holding a zero value.
	_, ok, err = ReadConfigKey("theme")
	require.NoError(t, err)
	require.False(t, ok)
}
