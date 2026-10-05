// Package theme holds the colour palette the UI draws with.
//
// Every colour in the interface resolves to one of a dozen named roles rather
// than to a literal, so a theme is a small table and a user override is one
// entry in it. The roles are semantic — what a colour *means* — because the same
// green is the ready dot, a passing build, added lines and an approving review,
// and a theme that had to name all four separately would let them drift apart.
package theme

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Role names a colour by what it means rather than by where it is used.
type Role string

const (
	// Accent is the interface's own colour: the selection bar, the tab
	// highlight, overlay borders and titles, and the menu's action keys.
	Accent Role = "accent"
	// AccentText is for text drawn ON Accent, so it has to contrast with it.
	AccentText Role = "accentText"
	// Success: the ready dot, a passing build, added lines, an approving review.
	Success Role = "success"
	// Danger: a failing build, removed lines, errors, conflicts, changes requested.
	Danger Role = "danger"
	// Warning: a build still running.
	Warning Role = "warning"
	// Info: diff hunk headers and help section headings.
	Info Role = "info"
	// Special: a merged pull request, which is neither pass nor fail.
	Special Role = "special"
	// Text is ordinary foreground text.
	Text Role = "text"
	// Muted is secondary text: paused sessions, hints, branch lines, no-PR.
	Muted Role = "muted"
	// Subtle is dimmer than Muted: separators and rules, which should be
	// present without being read.
	Subtle Role = "subtle"
	// SelectionBg and SelectionFg are the selected row's own pair. They are
	// roles rather than a shade of Accent because they must contrast with each
	// other, which no single hue can guarantee.
	SelectionBg Role = "selectionBg"
	SelectionFg Role = "selectionFg"
)

// Roles is every role, in a stable order for listing and validation.
var Roles = []Role{
	Accent, AccentText, Success, Danger, Warning, Info,
	Special, Text, Muted, Subtle, SelectionBg, SelectionFg,
}

// Pair is one role's colour. Light and Dark let a theme follow the terminal's
// background; a theme that means to look the same either way sets both.
type Pair struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// UnmarshalJSON accepts either a bare string, used for both backgrounds, or an
// object naming each. The bare form is what a user reaching for one override
// will write, and demanding the object form for it would be pure ceremony.
func (p *Pair) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, `"`) {
		var single string
		if err := jsonUnmarshal(data, &single); err != nil {
			return err
		}
		p.Light, p.Dark = single, single
		return nil
	}
	type raw Pair // avoids recursing back into this method
	var out raw
	if err := jsonUnmarshal(data, &out); err != nil {
		return err
	}
	*p = Pair(out)
	return nil
}

// Palette maps every role to a colour.
type Palette map[Role]Pair

// hexPattern is deliberately strict. An unparseable colour is not rendered as an
// error by lipgloss -- it silently falls back to the terminal default, so a typo
// would quietly blank part of the interface rather than announce itself.
var hexPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// ansiPattern matches the 0-255 terminal palette, which several built-in colours
// use and which is the right choice for a theme that wants to follow whatever
// the user's terminal is configured with.
var ansiPattern = regexp.MustCompile(`^(?:[0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)

// ValidColor reports whether a value is one lipgloss can actually resolve.
func ValidColor(v string) bool {
	return hexPattern.MatchString(v) || ansiPattern.MatchString(v)
}

// Validate reports every role a palette is missing or spells wrongly.
func (p Palette) Validate() []error {
	var errs []error
	for _, role := range Roles {
		pair, ok := p[role]
		if !ok {
			errs = append(errs, fmt.Errorf("role %q is not set", role))
			continue
		}
		for label, value := range map[string]string{"light": pair.Light, "dark": pair.Dark} {
			if !ValidColor(value) {
				errs = append(errs, fmt.Errorf("role %q has an unusable %s colour %q: want #rgb, #rrggbb, or 0-255", role, label, value))
			}
		}
	}
	return errs
}

// clone copies a palette so a caller's overrides cannot mutate a built-in theme,
// which is package state shared by every later resolution.
func (p Palette) clone() Palette {
	out := make(Palette, len(p))
	for role, pair := range p {
		out[role] = pair
	}
	return out
}

// current is the palette the UI draws with. It starts as the default so that a
// package whose styles are built at init -- before any config has been read --
// gets real colours rather than empty ones.
var current = Default.clone()

// hooks are called whenever the palette changes. Styles are built once, at
// package init, and hold a copy of their colour, so they have to be rebuilt
// rather than re-read. Each UI package registers its own rebuild in its init,
// which is what stops a new one from being silently left on the old palette.
var hooks []func()

// OnChange registers a rebuild hook. Safe to call from an init function.
func OnChange(f func()) {
	hooks = append(hooks, f)
	f()
}

// Set installs a palette and rebuilds every registered style.
func Set(p Palette) {
	current = p.clone()
	for _, hook := range hooks {
		hook()
	}
}

// Current returns a copy of the palette in use.
func Current() Palette { return current.clone() }

// Color returns one of this palette's colours, ready to hand to lipgloss.
//
// An unknown role resolves to the default palette's value rather than to nothing:
// a missing colour renders as the terminal default, which for a background role
// means an invisible row.
//
// A method as well as the package function below, so a caller can draw in a
// palette it is not using — the theme picker previews every theme's colours side
// by side, and installing each one in turn to render a swatch would mean
// mutating global state from inside a render.
func (p Palette) Color(role Role) lipgloss.TerminalColor {
	pair, ok := p[role]
	if !ok {
		pair = Default[role]
	}
	return lipgloss.AdaptiveColor{Light: pair.Light, Dark: pair.Dark}
}

// Color returns a role's colour from the palette in use.
func Color(role Role) lipgloss.TerminalColor {
	return current.Color(role)
}

// Swatch renders a palette as a row of colour blocks, in the order of Roles.
//
// Shared by the CLI listing and the in-app picker so the two cannot come to show
// different things, and defined here because it is the one piece of rendering
// that belongs to a palette rather than to a screen.
func Swatch(p Palette) string {
	var out strings.Builder
	for _, role := range Roles {
		out.WriteString(lipgloss.NewStyle().Background(p.Color(role)).Render("  "))
	}
	return out.String()
}
