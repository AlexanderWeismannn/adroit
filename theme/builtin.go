package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Default reproduces the colours the interface shipped with, exactly. It is the
// baseline every other theme is a variation on, and the fallback whenever a
// configured theme cannot be used, so its values are the literals that were
// previously spread through the ui packages rather than a fresh approximation.
var Default = Palette{
	Accent:      {Light: "#874BFD", Dark: "#7D56F4"},
	AccentText:  {Light: "#FFFFFF", Dark: "#FFFFFF"},
	Success:     {Light: "#51bd73", Dark: "#51bd73"},
	Danger:      {Light: "#de613e", Dark: "#de613e"},
	Warning:     {Light: "#b8860b", Dark: "#e3b341"},
	Info:        {Light: "#0ea5e9", Dark: "#0ea5e9"},
	Special:     {Light: "#8250df", Dark: "#a371f7"},
	Text:        {Light: "#1a1a1a", Dark: "#dddddd"},
	Muted:       {Light: "#A49FA5", Dark: "#777777"},
	Subtle:      {Light: "#DDDADA", Dark: "#3C3C3C"},
	SelectionBg: {Light: "#dde4f0", Dark: "#2b3040"},
	SelectionFg: {Light: "#1a1a1a", Dark: "#e8eaf0"},
}

// The built-ins below are the fun part. Each sets every role, because a partial
// theme would inherit the default's hues and look like neither.
var (
	dracula = Palette{
		Accent:      {Light: "#bd93f9", Dark: "#bd93f9"},
		AccentText:  {Light: "#282a36", Dark: "#282a36"},
		Success:     {Light: "#50fa7b", Dark: "#50fa7b"},
		Danger:      {Light: "#ff5555", Dark: "#ff5555"},
		Warning:     {Light: "#f1fa8c", Dark: "#f1fa8c"},
		Info:        {Light: "#8be9fd", Dark: "#8be9fd"},
		Special:     {Light: "#ff79c6", Dark: "#ff79c6"},
		Text:        {Light: "#282a36", Dark: "#f8f8f2"},
		Muted:       {Light: "#6272a4", Dark: "#6272a4"},
		Subtle:      {Light: "#c8c8d4", Dark: "#44475a"},
		SelectionBg: {Light: "#e8e4f5", Dark: "#44475a"},
		SelectionFg: {Light: "#282a36", Dark: "#f8f8f2"},
	}

	nord = Palette{
		Accent:      {Light: "#5e81ac", Dark: "#88c0d0"},
		AccentText:  {Light: "#eceff4", Dark: "#2e3440"},
		Success:     {Light: "#a3be8c", Dark: "#a3be8c"},
		Danger:      {Light: "#bf616a", Dark: "#bf616a"},
		Warning:     {Light: "#ebcb8b", Dark: "#ebcb8b"},
		Info:        {Light: "#81a1c1", Dark: "#81a1c1"},
		Special:     {Light: "#b48ead", Dark: "#b48ead"},
		Text:        {Light: "#2e3440", Dark: "#e5e9f0"},
		Muted:       {Light: "#7b88a1", Dark: "#7b88a1"},
		Subtle:      {Light: "#d8dee9", Dark: "#434c5e"},
		SelectionBg: {Light: "#dbe3ee", Dark: "#3b4252"},
		SelectionFg: {Light: "#2e3440", Dark: "#eceff4"},
	}

	gruvbox = Palette{
		Accent:      {Light: "#af3a03", Dark: "#fe8019"},
		AccentText:  {Light: "#fbf1c7", Dark: "#282828"},
		Success:     {Light: "#79740e", Dark: "#b8bb26"},
		Danger:      {Light: "#9d0006", Dark: "#fb4934"},
		Warning:     {Light: "#b57614", Dark: "#fabd2f"},
		Info:        {Light: "#427b58", Dark: "#8ec07c"},
		Special:     {Light: "#8f3f71", Dark: "#d3869b"},
		Text:        {Light: "#3c3836", Dark: "#ebdbb2"},
		Muted:       {Light: "#7c6f64", Dark: "#a89984"},
		Subtle:      {Light: "#d5c4a1", Dark: "#504945"},
		SelectionBg: {Light: "#ebdbb2", Dark: "#3c3836"},
		SelectionFg: {Light: "#3c3836", Dark: "#fbf1c7"},
	}

	catppuccin = Palette{
		Accent:      {Light: "#8839ef", Dark: "#cba6f7"},
		AccentText:  {Light: "#eff1f5", Dark: "#1e1e2e"},
		Success:     {Light: "#40a02b", Dark: "#a6e3a1"},
		Danger:      {Light: "#d20f39", Dark: "#f38ba8"},
		Warning:     {Light: "#df8e1d", Dark: "#f9e2af"},
		Info:        {Light: "#209fb5", Dark: "#89dceb"},
		Special:     {Light: "#ea76cb", Dark: "#f5c2e7"},
		Text:        {Light: "#4c4f69", Dark: "#cdd6f4"},
		Muted:       {Light: "#8c8fa1", Dark: "#9399b2"},
		Subtle:      {Light: "#ccd0da", Dark: "#45475a"},
		SelectionBg: {Light: "#e6e9ef", Dark: "#313244"},
		SelectionFg: {Light: "#4c4f69", Dark: "#cdd6f4"},
	}

	solarized = Palette{
		Accent:      {Light: "#268bd2", Dark: "#268bd2"},
		AccentText:  {Light: "#fdf6e3", Dark: "#002b36"},
		Success:     {Light: "#859900", Dark: "#859900"},
		Danger:      {Light: "#dc322f", Dark: "#dc322f"},
		Warning:     {Light: "#b58900", Dark: "#b58900"},
		Info:        {Light: "#2aa198", Dark: "#2aa198"},
		Special:     {Light: "#6c71c4", Dark: "#6c71c4"},
		Text:        {Light: "#073642", Dark: "#eee8d5"},
		Muted:       {Light: "#93a1a1", Dark: "#839496"},
		Subtle:      {Light: "#eee8d5", Dark: "#073642"},
		SelectionBg: {Light: "#eee8d5", Dark: "#073642"},
		SelectionFg: {Light: "#073642", Dark: "#fdf6e3"},
	}

	// matrix is the joke one, and it is a real test of the role split: with a
	// single hue, anything that relied on colour alone to tell two states apart
	// becomes unreadable. Everything stays legible here because every state also
	// has a glyph.
	matrix = Palette{
		Accent:      {Light: "#008f11", Dark: "#00ff41"},
		AccentText:  {Light: "#000000", Dark: "#000000"},
		Success:     {Light: "#008f11", Dark: "#00ff41"},
		Danger:      {Light: "#8f0000", Dark: "#ff5f5f"},
		Warning:     {Light: "#6b8f00", Dark: "#9dff41"},
		Info:        {Light: "#008f6b", Dark: "#41ffb0"},
		Special:     {Light: "#006b8f", Dark: "#41d0ff"},
		Text:        {Light: "#003b00", Dark: "#00cc33"},
		Muted:       {Light: "#4c8f4c", Dark: "#008f11"},
		Subtle:      {Light: "#bfe0bf", Dark: "#004f0a"},
		SelectionBg: {Light: "#d6f5d6", Dark: "#012b06"},
		SelectionFg: {Light: "#003b00", Dark: "#00ff41"},
	}

	// mono uses the terminal's own 0-255 palette, so it inherits whatever colour
	// scheme the terminal is already set to instead of imposing one.
	mono = Palette{
		Accent:      {Light: "4", Dark: "12"},
		AccentText:  {Light: "15", Dark: "0"},
		Success:     {Light: "2", Dark: "10"},
		Danger:      {Light: "1", Dark: "9"},
		Warning:     {Light: "3", Dark: "11"},
		Info:        {Light: "6", Dark: "14"},
		Special:     {Light: "5", Dark: "13"},
		Text:        {Light: "0", Dark: "7"},
		Muted:       {Light: "8", Dark: "8"},
		Subtle:      {Light: "7", Dark: "8"},
		SelectionBg: {Light: "7", Dark: "8"},
		SelectionFg: {Light: "0", Dark: "15"},
	}
)

// builtins is the name a user writes in config.json.
var builtins = map[string]Palette{
	"default":    Default,
	"dracula":    dracula,
	"nord":       nord,
	"gruvbox":    gruvbox,
	"catppuccin": catppuccin,
	"solarized":  solarized,
	"matrix":     matrix,
	"mono":       mono,
}

// Names lists the built-in themes, sorted, with default first so the list reads
// as "the one you have, and the alternatives".
func Names() []string {
	rest := make([]string, 0, len(builtins)-1)
	for name := range builtins {
		if name != "default" {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append([]string{"default"}, rest...)
}

// Builtin returns a copy of a built-in theme.
func Builtin(name string) (Palette, bool) {
	p, ok := builtins[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, false
	}
	return p.clone(), true
}

// Resolve turns a configured theme name and a set of per-role overrides into the
// palette to draw with, along with any complaints about it.
//
// It never fails: a bad name or a bad colour yields warnings and the nearest
// usable palette. A theme is decoration, and refusing to start over a typo in one
// would be wildly out of proportion — but the warnings must be surfaced, or a
// misspelled role would silently do nothing at all.
func Resolve(name string, overrides map[string]Pair) (Palette, []error) {
	var warnings []error

	base := Default.clone()
	if trimmed := strings.TrimSpace(name); trimmed != "" && !strings.EqualFold(trimmed, "default") {
		if p, ok := Builtin(trimmed); ok {
			base = p
		} else {
			warnings = append(warnings, fmt.Errorf("unknown theme %q; using default (available: %s)",
				name, strings.Join(Names(), ", ")))
		}
	}

	valid := make(map[Role]bool, len(Roles))
	for _, role := range Roles {
		valid[role] = true
	}

	// Sorted so the warnings come out in a stable order rather than map order.
	names := make([]string, 0, len(overrides))
	for key := range overrides {
		names = append(names, key)
	}
	sort.Strings(names)

	for _, key := range names {
		role := Role(key)
		if !valid[role] {
			warnings = append(warnings, fmt.Errorf("unknown colour role %q; ignoring (available: %s)",
				key, joinRoles()))
			continue
		}
		pair := overrides[key]
		// A half-specified override fills the unset side from the theme beneath,
		// so overriding only the dark colour leaves light alone rather than
		// blanking it.
		beneath := base[role]
		if pair.Light == "" {
			pair.Light = beneath.Light
		}
		if pair.Dark == "" {
			pair.Dark = beneath.Dark
		}
		if !ValidColor(pair.Light) || !ValidColor(pair.Dark) {
			warnings = append(warnings, fmt.Errorf(
				"colour role %q: %q/%q is not a usable colour; keeping the theme's own (want #rgb, #rrggbb, or 0-255)",
				key, pair.Light, pair.Dark))
			continue
		}
		base[role] = pair
	}

	// A built-in that forgot a role would render it as the terminal default,
	// which for a background is an invisible row. Backfill rather than ship that.
	for _, role := range Roles {
		if _, ok := base[role]; !ok {
			base[role] = Default[role]
			warnings = append(warnings, fmt.Errorf("theme %q does not set role %q; using the default's", name, role))
		}
	}

	return base, warnings
}

func joinRoles() string {
	out := make([]string, 0, len(Roles))
	for _, role := range Roles {
		out = append(out, string(role))
	}
	return strings.Join(out, ", ")
}
