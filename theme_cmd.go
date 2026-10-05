package main

import (
	"encoding/json"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/theme"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	themeSetLight string
	themeSetDark  string
)

// The theme commands validate strictly and refuse on a bad value, which is the
// opposite polarity to theme.Resolve at startup. That is deliberate: at startup a
// typo must not stop the app from running, so it warns and falls back; here the
// user is explicitly asking to set something, and quietly storing a value that
// will be ignored forever is worse than saying no.
var themeCmd = &cobra.Command{
	Use:     "theme",
	Aliases: []string{"themes"},
	Short:   "Show, list, and change the colour theme",
	Long: "Run without arguments to list the built-in themes rendered in their own colours.\n" +
		"Use `theme use <name>` to switch, and `theme set <role> <colour>` to override one colour.",
	// A rejected colour is not a usage error, so printing the whole usage block
	// buries the one line that says what was wrong. Cobra checks these on the
	// command and its parents, so the subcommands inherit them.
	SilenceUsage: true,
	// main prints the error itself; without this cobra prints it too.
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error { return listThemes(cmd.OutOrStdout()) })
	},
}

var themeUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch to a built-in theme",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error {
			name := strings.ToLower(strings.TrimSpace(args[0]))
			if _, ok := theme.Builtin(name); !ok {
				return fmt.Errorf("unknown theme %q; available: %s", args[0], strings.Join(theme.Names(), ", "))
			}
			if err := config.UpdateConfigFile(map[string]any{"theme": name}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Theme set to %s. Restart cs to see it.\n", name)
			return nil
		})
	},
}

var themeSetCmd = &cobra.Command{
	Use:   "set <role> [colour]",
	Short: "Override one colour role",
	Long: "Give one colour to use for both terminal backgrounds, or use --light and --dark\n" +
		"to give each. A colour is #rgb, #rrggbb, or a terminal palette number 0-255.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error {
			role, err := parseRole(args[0])
			if err != nil {
				return err
			}

			light, dark := themeSetLight, themeSetDark
			if len(args) == 2 {
				if light != "" || dark != "" {
					return fmt.Errorf("give a colour or --light/--dark, not both")
				}
				light, dark = args[1], args[1]
			}
			if light == "" && dark == "" {
				return fmt.Errorf("give a colour, or at least one of --light and --dark")
			}
			for label, value := range map[string]string{"light": light, "dark": dark} {
				if value != "" && !theme.ValidColor(value) {
					return fmt.Errorf("%q is not a usable %s colour: want #rgb, #rrggbb, or 0-255", value, label)
				}
			}

			colors, err := currentOverrides()
			if err != nil {
				return err
			}
			// Merge onto whatever is already stored for this role, so setting only
			// --dark leaves an existing --light alone rather than dropping it.
			pair := colors[string(role)]
			if light != "" {
				pair.Light = light
			}
			if dark != "" {
				pair.Dark = dark
			}
			// A half-specified override falls back to the theme beneath at load
			// time, but storing it half-empty makes `theme show` unreadable, so
			// fill from the theme now.
			base := resolvedBase()
			if pair.Light == "" {
				pair.Light = base[role].Light
			}
			if pair.Dark == "" {
				pair.Dark = base[role].Dark
			}
			colors[string(role)] = pair

			if err := config.UpdateConfigFile(map[string]any{"colors": colors}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now light %s / dark %s. Restart cs to see it.\n",
				role, pair.Light, pair.Dark)
			return nil
		})
	},
}

var themeUnsetCmd = &cobra.Command{
	Use:   "unset <role>...",
	Short: "Remove colour overrides, keeping the theme",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error {
			colors, err := currentOverrides()
			if err != nil {
				return err
			}
			for _, arg := range args {
				role, err := parseRole(arg)
				if err != nil {
					return err
				}
				if _, ok := colors[string(role)]; !ok {
					return fmt.Errorf("no override is set for %q", role)
				}
				delete(colors, string(role))
			}

			// An empty object would be indistinguishable from overrides that had
			// been set, so remove the key rather than storing "colors": {}.
			var value any = colors
			if len(colors) == 0 {
				value = nil
			}
			if err := config.UpdateConfigFile(map[string]any{"colors": value}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s. Restart cs to see it.\n", strings.Join(args, ", "))
			return nil
		})
	},
}

var themeResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Go back to the default theme with no overrides",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error {
			// nil deletes the key, so the config returns to not mentioning colour
			// at all rather than pinning "default" forever.
			if err := config.UpdateConfigFile(map[string]any{"theme": nil, "colors": nil}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Theme reset to default. Restart cs to see it.")
			return nil
		})
	},
}

var themeShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the theme in use, and every colour it resolves to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withThemeLog(func() error {
			cfg := config.LoadConfig()
			name := strings.TrimSpace(cfg.Theme)
			if name == "" {
				name = "default"
			}
			palette, warnings := theme.Resolve(cfg.Theme, cfg.Colors)
			theme.Set(palette)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Theme: %s\n\n", accented(name))
			for _, role := range theme.Roles {
				pair := palette[role]
				marker := "  "
				if _, overridden := cfg.Colors[string(role)]; overridden {
					marker = "* "
				}
				fmt.Fprintf(out, "%s%s %s  light %-9s dark %s\n",
					marker,
					lipgloss.NewStyle().Background(theme.Color(role)).Render("    "),
					lipgloss.NewStyle().Foreground(theme.Color(theme.Muted)).Render(fmt.Sprintf("%-12s", role)),
					pair.Light, pair.Dark)
			}
			if len(cfg.Colors) > 0 {
				fmt.Fprintf(out, "\n* overridden in config.json\n")
			}
			for _, warning := range warnings {
				fmt.Fprintf(out, "\nwarning: %v\n", warning)
			}
			return nil
		})
	},
}

// listThemes renders each theme in its own colours, which is the only way to
// choose one: a list of names says nothing about what they look like.
func listThemes(out io.Writer) error {
	cfg := config.LoadConfig()
	active := strings.ToLower(strings.TrimSpace(cfg.Theme))
	if active == "" {
		active = "default"
	}

	// Each row is drawn in its own palette without installing it, so listing has
	// no side effect on the palette the process is using.
	for _, name := range theme.Names() {
		palette, _ := theme.Builtin(name)

		marker := "  "
		if name == active {
			marker = "→ "
		}
		label := lipgloss.NewStyle().Bold(true).
			Foreground(palette.Color(theme.Accent)).Render(fmt.Sprintf("%-11s", name))
		fmt.Fprintf(out, "%s%s %s\n", marker, label, theme.Swatch(palette))
	}

	// The hints follow the name the binary was invoked under, so an adroit
	// symlinked to cs prints the form the user actually types. Widths are computed
	// rather than written in, since the name is no longer a fixed two characters.
	hints := [][2]string{
		{"theme use <name>", "switch theme"},
		{"theme set <role> <colour>", "override one colour"},
		{"theme show", "what is in use now"},
	}
	width := 0
	for i, h := range hints {
		hints[i][0] = invokedName() + " " + h[0]
		if n := len(hints[i][0]); n > width {
			width = n
		}
	}
	fmt.Fprintln(out)
	for _, h := range hints {
		fmt.Fprintf(out, "  %-*s  %s\n", width, h[0], h[1])
	}
	fmt.Fprintf(out, "\n  roles: %s\n", strings.Join(roleNames(), ", "))
	return nil
}

func accented(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent)).Render(s)
}

// parseRole resolves a role name, listing the alternatives when it does not.
func parseRole(name string) (theme.Role, error) {
	trimmed := strings.TrimSpace(name)
	for _, role := range theme.Roles {
		if strings.EqualFold(string(role), trimmed) {
			return role, nil
		}
	}
	return "", fmt.Errorf("unknown colour role %q; available: %s", name, strings.Join(roleNames(), ", "))
}

// currentOverrides reads the stored overrides, so a set or unset edits them
// rather than replacing the lot.
func currentOverrides() (map[string]theme.Pair, error) {
	raw, ok, err := config.ReadConfigKey("colors")
	if err != nil {
		return nil, fmt.Errorf("failed to read the current colours: %w", err)
	}
	colors := map[string]theme.Pair{}
	if ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &colors); err != nil {
			return nil, fmt.Errorf(`the "colors" section of config.json could not be read: %w`, err)
		}
	}
	return colors, nil
}

// resolvedBase is the palette an override lands on top of.
func resolvedBase() theme.Palette {
	cfg := config.LoadConfig()
	if p, ok := theme.Builtin(cfg.Theme); ok {
		return p
	}
	return theme.Default
}

// roleNames lists the overridable colour roles.
func roleNames() []string {
	out := make([]string, 0, len(theme.Roles))
	for _, role := range theme.Roles {
		out = append(out, string(role))
	}
	sort.Strings(out)
	return out
}

// withThemeLog runs a theme command with logging initialised. LoadConfig and
// UpdateConfigFile both log, and without this their output goes nowhere useful.
func withThemeLog(f func() error) error {
	log.Initialize(false)
	defer log.Close()
	return f()
}

func init() {
	themeSetCmd.Flags().StringVar(&themeSetLight, "light", "", "colour to use on a light terminal background")
	themeSetCmd.Flags().StringVar(&themeSetDark, "dark", "", "colour to use on a dark terminal background")

	themeCmd.AddCommand(themeUseCmd, themeSetCmd, themeUnsetCmd, themeResetCmd, themeShowCmd)

	// Cobra checks these on the ROOT and on the command it actually ran, never on
	// an intermediate parent — so setting them on themeCmd alone silences nothing
	// for `theme use`. Applied to each child rather than to the root, which would
	// change how every other command reports a failure.
	for _, cmd := range themeCmd.Commands() {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
	}
}

// invokedName is the name this binary was invoked under, for use in help text.
// binName is set from os.Args[0] in main, so it is empty when this is reached
// any other way (a test, say) -- fall back to the project's own name.
func invokedName() string {
	if binName == "" {
		return "adroit"
	}
	return binName
}
