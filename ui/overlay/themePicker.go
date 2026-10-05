package overlay

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ThemePicker chooses a colour theme, previewing each one on the interface
// behind it as the cursor moves.
//
// It holds no palette of its own and installs nothing: the caller owns that, so
// it also owns putting the original back when the picker is cancelled. Keeping
// the two apart is what makes cancel reliable — a component that both previewed
// and remembered would have to get the restore right in every exit path,
// including the ones it does not control.
type ThemePicker struct {
	names   []string
	cursor  int
	initial int
}

// NewThemePicker opens on the theme currently in use, so the first thing the
// cursor sits on is what the user already has and arrowing away is a comparison
// rather than a leap.
func NewThemePicker(active string) *ThemePicker {
	names := theme.Names()
	picker := &ThemePicker{names: names}
	for i, name := range names {
		if strings.EqualFold(name, strings.TrimSpace(active)) {
			picker.cursor = i
			break
		}
	}
	picker.initial = picker.cursor
	return picker
}

// Selected is the theme under the cursor.
func (t *ThemePicker) Selected() string { return t.names[t.cursor] }

// Changed reports whether the cursor has left the theme it opened on, so the
// caller can skip writing a config file for a choice that changes nothing.
func (t *ThemePicker) Changed() bool { return t.cursor != t.initial }

// PickerAction is what a keypress asked the caller to do.
type PickerAction int

const (
	// PickerNone: the key did nothing.
	PickerNone PickerAction = iota
	// PickerPreview: the selection moved; show it.
	PickerPreview
	// PickerConfirm: keep the selection and save it.
	PickerConfirm
	// PickerCancel: put back whatever was in use before.
	PickerCancel
)

func (t *ThemePicker) HandleKeyPress(msg tea.KeyMsg) PickerAction {
	switch msg.Type {
	case tea.KeyUp:
		return t.move(-1)
	case tea.KeyDown:
		return t.move(1)
	case tea.KeyEnter:
		return PickerConfirm
	case tea.KeyEsc:
		return PickerCancel
	case tea.KeyRunes:
		// The list is short and every name is distinct in its first letter or
		// two, so j/k and a first-letter jump are both cheaper than arrowing.
		switch string(msg.Runes) {
		case "k":
			return t.move(-1)
		case "j":
			return t.move(1)
		case "q":
			return PickerCancel
		}
		return t.jumpTo(strings.ToLower(string(msg.Runes)))
	}
	return PickerNone
}

// move returns PickerNone at either end, so the caller does not re-render and
// re-install a palette that has not changed.
func (t *ThemePicker) move(delta int) PickerAction {
	next := t.cursor + delta
	if next < 0 || next >= len(t.names) {
		return PickerNone
	}
	t.cursor = next
	return PickerPreview
}

// jumpTo moves to the next theme starting with the given letter, wrapping, so
// pressing the same letter cycles through the themes that share it.
func (t *ThemePicker) jumpTo(prefix string) PickerAction {
	if prefix == "" {
		return PickerNone
	}
	for offset := 1; offset <= len(t.names); offset++ {
		i := (t.cursor + offset) % len(t.names)
		if strings.HasPrefix(t.names[i], prefix) {
			if i == t.cursor {
				return PickerNone
			}
			t.cursor = i
			return PickerPreview
		}
	}
	return PickerNone
}

func (t *ThemePicker) Render() string {
	var body strings.Builder
	body.WriteString(themeTitleStyle.Render("Theme"))
	body.WriteString(themeHintStyle.Render("   ↑↓ preview · enter keep · esc cancel"))
	body.WriteString("\n\n")

	width := 0
	for _, name := range t.names {
		if len(name) > width {
			width = len(name)
		}
	}

	for i, name := range t.names {
		// Each row is drawn in its OWN palette rather than the one being
		// previewed, so the list stays a comparison: switching to a dim theme
		// must not dim every other theme's swatch along with it.
		palette, _ := theme.Builtin(name)

		marker := "  "
		label := lipgloss.NewStyle().Foreground(palette.Color(theme.Muted))
		if i == t.cursor {
			marker = lipgloss.NewStyle().Foreground(palette.Color(theme.Accent)).Render("▸ ")
			label = lipgloss.NewStyle().Foreground(palette.Color(theme.Accent)).Bold(true)
		}

		body.WriteString(marker)
		body.WriteString(label.Render(padRight(name, width)))
		body.WriteString("  ")
		body.WriteString(theme.Swatch(palette))
		if i != len(t.names)-1 {
			body.WriteString("\n")
		}
	}

	return themeBorderStyle.Render(body.String())
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

var (
	themeBorderStyle lipgloss.Style
	themeTitleStyle  lipgloss.Style
	themeHintStyle   lipgloss.Style
)

func init() { theme.OnChange(applyThemePickerTheme) }

// The picker's own chrome follows the theme being previewed, so choosing one
// shows what its accent and border will actually look like.
func applyThemePickerTheme() {
	themeBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Color(theme.Accent)).
		Padding(1, 2)
	themeTitleStyle = lipgloss.NewStyle().
		Foreground(theme.Color(theme.Accent)).
		Bold(true)
	themeHintStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
}
