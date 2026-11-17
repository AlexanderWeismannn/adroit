package theme

import (
	"encoding/json"
	"fmt"
	"math"
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

// The terminal background each built-in is drawn on: the theme's own paper
// where it has one, white and Windows Terminal's default black where it does
// not. mono is absent on purpose -- it uses the terminal's 0-255 palette, whose
// colours are whatever the terminal says they are.
var referenceBackground = map[string]Pair{
	"default":    {Light: "#ffffff", Dark: "#0c0c0c"},
	"dracula":    {Light: "#f8f8f2", Dark: "#282a36"},
	"nord":       {Light: "#eceff4", Dark: "#2e3440"},
	"gruvbox":    {Light: "#fbf1c7", Dark: "#282828"},
	"catppuccin": {Light: "#eff1f5", Dark: "#1e1e2e"},
	"solarized":  {Light: "#fdf6e3", Dark: "#002b36"},
	"matrix":     {Light: "#ffffff", Dark: "#000000"},
	"slate":      {Light: "#f6f4ef", Dark: "#14171c"},
}

// relativeLuminance is WCAG 2's, of a #rrggbb colour.
func relativeLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	var rgb [3]float64
	for i := range rgb {
		var v int
		_, err := fmt.Sscanf(hex[1+2*i:3+2*i], "%02x", &v)
		require.NoError(t, err, hex)
		c := float64(v) / 255
		if c <= 0.03928 {
			rgb[i] = c / 12.92
		} else {
			rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := relativeLuminance(t, a), relativeLuminance(t, b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// Every role drawn as text must be readable on the background it is drawn on,
// in both halves of every built-in, and the selected row's band must be visible
// against the rows around it. Measured, several were not: dracula's light-half
// warning was 1.1:1 (yellow on white), nord's 1.6:1, the default's light-half
// success 2.4:1 and muted 2.6:1, and solarized's selection band was its Subtle
// colour, 1.15:1 -- the selected row read by its bold title alone.
func TestEveryBuiltinIsReadable(t *testing.T) {
	text := []Role{Text, Muted, Accent, Success, Danger, Warning, Info, Special}
	for _, name := range Names() {
		if name == "mono" {
			continue
		}
		palette, _ := Builtin(name)
		bg, ok := referenceBackground[name]
		require.True(t, ok, "%s has no reference background: add the terminal background it is designed for", name)
		for _, half := range []struct {
			name string
			pick func(Pair) string
		}{{"light", func(p Pair) string { return p.Light }}, {"dark", func(p Pair) string { return p.Dark }}} {
			paper := half.pick(bg)
			for _, role := range text {
				c := contrast(t, half.pick(palette[role]), paper)
				require.GreaterOrEqual(t, c, 3.0, "%s %s: %s %s on %s is %.2f:1", name, half.name, role, half.pick(palette[role]), paper, c)
			}
			band := half.pick(palette[SelectionBg])
			c := contrast(t, band, paper)
			require.GreaterOrEqual(t, c, 1.3, "%s %s: the selection band %s on %s is %.2f:1", name, half.name, band, paper, c)
			c = contrast(t, half.pick(palette[SelectionFg]), band)
			require.GreaterOrEqual(t, c, 4.5, "%s %s: selected text on its band is %.2f:1", name, half.name, c)
		}
	}
}
