package overlay

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestThemePickerOpensOnTheThemeInUse(t *testing.T) {
	// So the first thing under the cursor is what the user already has, and
	// arrowing away is a comparison rather than a leap.
	require.Equal(t, "dracula", NewThemePicker("dracula").Selected())
	require.Equal(t, "dracula", NewThemePicker("  DRACULA  ").Selected())

	// An unrecognised or empty setting falls back to the first entry, which
	// Names() guarantees is "default".
	require.Equal(t, "default", NewThemePicker("").Selected())
	require.Equal(t, "default", NewThemePicker("nosuchtheme").Selected())
}

func TestThemePickerNavigation(t *testing.T) {
	up := tea.KeyMsg{Type: tea.KeyUp}
	down := tea.KeyMsg{Type: tea.KeyDown}

	p := NewThemePicker("default") // index 0
	// At the top edge: no move, and no action, so the caller does not re-install
	// a palette that has not changed.
	require.Equal(t, PickerNone, p.HandleKeyPress(up))
	require.Equal(t, "default", p.Selected())

	require.Equal(t, PickerPreview, p.HandleKeyPress(down))
	require.NotEqual(t, "default", p.Selected())

	// Walk to the bottom and confirm the far edge behaves the same way.
	for i := 0; i < len(theme.Names()); i++ {
		p.HandleKeyPress(down)
	}
	require.Equal(t, PickerNone, p.HandleKeyPress(down))

	require.Equal(t, PickerConfirm, p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEnter}))
	require.Equal(t, PickerCancel, p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyEsc}))
}

// Confirming without having moved must not write a config file for a choice that
// changes nothing.
func TestThemePickerReportsWhetherTheChoiceMoved(t *testing.T) {
	p := NewThemePicker("default")
	require.False(t, p.Changed())

	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyDown})
	require.True(t, p.Changed())

	p.HandleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
	require.False(t, p.Changed(), "back where it started is not a change")
}

func TestThemePickerFirstLetterJump(t *testing.T) {
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	p := NewThemePicker("default")
	require.Equal(t, PickerPreview, p.HandleKeyPress(runes("g")))
	require.Equal(t, "gruvbox", p.Selected())

	// j and k stay navigation rather than jumping to a theme starting with them.
	require.Equal(t, PickerPreview, p.HandleKeyPress(runes("j")))
	require.Equal(t, "matrix", p.Selected())
	require.Equal(t, PickerPreview, p.HandleKeyPress(runes("k")))
	require.Equal(t, "gruvbox", p.Selected())

	// A letter no theme starts with leaves the cursor alone.
	require.Equal(t, PickerNone, p.HandleKeyPress(runes("z")))
	require.Equal(t, "gruvbox", p.Selected())

	// The letter of the theme already selected finds nothing else and stays put.
	require.Equal(t, PickerNone, p.HandleKeyPress(runes("g")))
}

// Each row is drawn in its own palette, so previewing a dim theme must not dim
// every other theme's swatch along with it — the list is a comparison, and it
// stops being one the moment the installed palette bleeds into the rows.
func TestThemePickerRowsKeepTheirOwnColours(t *testing.T) {
	// Without this the test renders under the non-TTY profile, which strips every
	// colour and would make the assertion pass for the wrong reason.
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	before := theme.Current()
	t.Cleanup(func() {
		theme.Set(before)
		lipgloss.SetColorProfile(previous)
	})

	def, _ := theme.Builtin("default")
	theme.Set(def)
	withDefault := NewThemePicker("default").Render()

	matrix, _ := theme.Builtin("matrix")
	theme.Set(matrix)
	withMatrix := NewThemePicker("default").Render()

	// The chrome follows the installed theme, so the two renders differ...
	require.NotEqual(t, withDefault, withMatrix)
	// ...but every theme's own swatch is unchanged in both.
	for _, name := range theme.Names() {
		palette, _ := theme.Builtin(name)
		swatch := theme.Swatch(palette)
		require.Contains(t, withDefault, swatch, name)
		require.Contains(t, withMatrix, swatch, name)
	}
}
