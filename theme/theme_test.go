package theme

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A theme that forgets a role renders it as the terminal default, which for a
// background role is an invisible row — so every built-in must set all of them,
// with colours lipgloss can actually parse. This is the guard against a new
// theme being added with a gap nobody notices until they switch to it.
func TestEveryBuiltinIsComplete(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			require.True(t, ok, "Names() offered a theme Builtin() does not know")
			require.Empty(t, palette.Validate())
		})
	}
}

// Built-ins are package state shared by every later resolution, so a caller's
// overrides must not reach them.
func TestResolveDoesNotMutateABuiltin(t *testing.T) {
	before, _ := Builtin("dracula")
	_, warnings := Resolve("dracula", map[string]Pair{"accent": {Light: "#000000", Dark: "#000000"}})
	require.Empty(t, warnings)

	after, _ := Builtin("dracula")
	require.Equal(t, before[Accent], after[Accent])
}

func TestResolve(t *testing.T) {
	t.Run("an unknown theme warns and falls back", func(t *testing.T) {
		palette, warnings := Resolve("nosuchtheme", nil)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Error(), "unknown theme")
		require.Equal(t, Default[Accent], palette[Accent])
	})

	t.Run("an empty name is the default, silently", func(t *testing.T) {
		palette, warnings := Resolve("", nil)
		require.Empty(t, warnings)
		require.Equal(t, Default, palette)
	})

	t.Run("a named theme is used", func(t *testing.T) {
		palette, warnings := Resolve("dracula", nil)
		require.Empty(t, warnings)
		require.NotEqual(t, Default[Accent], palette[Accent])
	})

	t.Run("an override replaces one role and leaves the rest", func(t *testing.T) {
		palette, warnings := Resolve("nord", map[string]Pair{"accent": {Light: "#ff0000", Dark: "#ff0000"}})
		require.Empty(t, warnings)
		require.Equal(t, Pair{Light: "#ff0000", Dark: "#ff0000"}, palette[Accent])
		nord, _ := Builtin("nord")
		require.Equal(t, nord[Success], palette[Success])
	})

	t.Run("a half override fills the other side from the theme beneath", func(t *testing.T) {
		// Overriding only the dark colour must leave light alone rather than
		// blanking it, which would render as the terminal default.
		nord, _ := Builtin("nord")
		palette, warnings := Resolve("nord", map[string]Pair{"accent": {Dark: "#ff0000"}})
		require.Empty(t, warnings)
		require.Equal(t, "#ff0000", palette[Accent].Dark)
		require.Equal(t, nord[Accent].Light, palette[Accent].Light)
	})

	t.Run("an unknown role warns rather than doing nothing quietly", func(t *testing.T) {
		_, warnings := Resolve("default", map[string]Pair{"acccent": {Light: "#ff0000", Dark: "#ff0000"}})
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Error(), "unknown colour role")
	})

	t.Run("an unusable colour warns and keeps the theme's own", func(t *testing.T) {
		// lipgloss does not report a bad colour, it silently renders the terminal
		// default — so this has to be caught here or not at all.
		palette, warnings := Resolve("default", map[string]Pair{"accent": {Light: "burgundy", Dark: "burgundy"}})
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Error(), "not a usable colour")
		require.Equal(t, Default[Accent], palette[Accent])
	})

	t.Run("warnings are ordered, not map-ordered", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			_, warnings := Resolve("default", map[string]Pair{"zzz": {}, "aaa": {}})
			require.Len(t, warnings, 2)
			require.Contains(t, warnings[0].Error(), `"aaa"`)
			require.Contains(t, warnings[1].Error(), `"zzz"`)
		}
	})
}

func TestValidColor(t *testing.T) {
	for _, ok := range []string{"#fff", "#FFFFFF", "#874BFD", "0", "62", "255"} {
		require.True(t, ValidColor(ok), ok)
	}
	for _, bad := range []string{"", "fff", "#ffff", "#gggggg", "256", "-1", "rebeccapurple", "62 "} {
		require.False(t, ValidColor(bad), bad)
	}
}

// A user reaching for one override will write a bare string; demanding the
// object form for it would be pure ceremony.
func TestPairAcceptsBothSpellings(t *testing.T) {
	var bare struct {
		C Pair `json:"c"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"c":"#ff0000"}`), &bare))
	require.Equal(t, Pair{Light: "#ff0000", Dark: "#ff0000"}, bare.C)

	var split struct {
		C Pair `json:"c"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"c":{"light":"#111111","dark":"#222222"}}`), &split))
	require.Equal(t, Pair{Light: "#111111", Dark: "#222222"}, split.C)
}

// Styles hold a copy of their colour, so a theme change is only real if the
// registered rebuilds actually run.
func TestSetRunsTheRebuildHooks(t *testing.T) {
	before := Current()
	t.Cleanup(func() { Set(before) })

	var ran int
	OnChange(func() { ran++ }) // OnChange runs it once immediately
	require.Equal(t, 1, ran)

	dracula, _ := Builtin("dracula")
	Set(dracula)
	require.Equal(t, 2, ran)
	require.Equal(t, dracula[Accent], Current()[Accent])
}

// Names is what the themes command prints and what an error message offers.
func TestNamesLeadsWithDefault(t *testing.T) {
	names := Names()
	require.Equal(t, "default", names[0])
	require.Len(t, names, len(builtins))
	require.True(t, strings.Contains(strings.Join(names, ","), "dracula"))
}
