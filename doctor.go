package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"

	"github.com/spf13/cobra"
)

// doctor exists because every missing dependency used to surface as something
// else: no tmux and no claude both read "timed out waiting for tmux session",
// and no gh just left the CI column blank forever. A new machine should get one
// list of what is missing and the command that installs it.
var doctorCmd = &cobra.Command{
	Use:           "doctor",
	Short:         "Check that the tools Adroit depends on are installed",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		log.Initialize(false)
		defer log.CloseQuietly()

		cfg := config.LoadConfig()
		program := cfg.GetProgram()
		if programFlag != "" {
			program = programFlag
		}
		if failed := runDoctor(cmd.OutOrStdout(), program); failed > 0 {
			return fmt.Errorf("%d required check(s) failed", failed)
		}
		return nil
	},
}

type checkLevel int

const (
	levelRequired checkLevel = iota
	levelOptional
)

type doctorCheck struct {
	name  string
	level checkLevel
	// why is shown when the check fails, so it says what stops working.
	why  string
	run  func() (detail string, ok bool)
	hint func() string
}

func runDoctor(w io.Writer, program string) (failed int) {
	checks := []doctorCheck{
		{
			name:  "git",
			level: levelRequired,
			why:   "every session is a git worktree",
			run:   versionOf("git", "--version"),
			hint:  func() string { return pkgHint("git", "git") },
		},
		{
			name:  "tmux",
			level: levelRequired,
			why:   "every session runs in its own tmux session",
			run:   versionOf("tmux", "-V"),
			hint:  func() string { return pkgHint("tmux", "tmux") },
		},
		{
			name:  "agent program",
			level: levelRequired,
			why:   "this is what each session runs (`default_program` in the config, or -p)",
			run: func() (string, bool) {
				path, err := resolveProgram(program)
				if errors.Is(err, errUnverifiable) {
					// Reported, not failed: the shell may well find it.
					return fmt.Sprintf("%s (%q %v)", program, path, err), true
				}
				if err != nil {
					return fmt.Sprintf("%q not found", program), false
				}
				if fields := strings.Fields(program); len(fields) > 0 && fields[0] == path {
					return program, true
				}
				return fmt.Sprintf("%s -> %s", program, path), true
			},
			hint: func() string { return programHint(program) },
		},
		{
			name:  "gh",
			level: levelOptional,
			why:   "CI status, pull-request state and `g` need it",
			run: func() (string, bool) {
				if _, err := exec.LookPath("gh"); err != nil {
					return "not installed", false
				}
				if err := exec.Command("gh", "auth", "status").Run(); err != nil {
					return "installed but not logged in", false
				}
				return "installed and logged in", true
			},
			hint: func() string {
				if _, err := exec.LookPath("gh"); err == nil {
					return "gh auth login"
				}
				return pkgHint("gh", "gh") + " && gh auth login"
			},
		},
		{
			name:  "process inspection",
			level: levelOptional,
			why:   "the dev stack's `own_cwd` check needs ss or lsof to see who owns a port",
			run: func() (string, bool) {
				for _, tool := range []string{"ss", "lsof"} {
					if path, err := exec.LookPath(tool); err == nil {
						return path, true
					}
				}
				return "neither ss nor lsof found", false
			},
			hint: func() string { return pkgHint("lsof", "lsof") },
		},
	}
	if runtime.GOOS == "linux" {
		checks = append(checks, doctorCheck{
			name:  "clipboard",
			level: levelOptional,
			why:   "checkout copies the branch name to the clipboard",
			run: func() (string, bool) {
				for _, tool := range []string{"clip.exe", "wl-copy", "xclip", "xsel"} {
					if path, err := exec.LookPath(tool); err == nil {
						return path, true
					}
				}
				return "none of clip.exe, wl-copy, xclip, xsel found", false
			},
			hint: func() string { return pkgHint("xclip", "xclip") },
		})
	}

	fmt.Fprintf(w, "%s doctor (%s, %s/%s)\n\n", binName, versionString(), runtime.GOOS, runtime.GOARCH)
	for _, c := range checks {
		detail, ok := c.run()
		mark := "ok  "
		if !ok {
			if c.level == levelRequired {
				mark = "FAIL"
				failed++
			} else {
				mark = "warn"
			}
		}
		fmt.Fprintf(w, "  [%s] %-20s %s\n", mark, c.name, detail)
		if !ok {
			fmt.Fprintf(w, "         %-20s %s\n", "", c.why)
			if hint := c.hint(); hint != "" {
				fmt.Fprintf(w, "         %-20s fix: %s\n", "", hint)
			}
		}
	}

	fmt.Fprintln(w)
	if dir, err := config.GetConfigDir(); err == nil {
		path := filepath.Join(dir, config.ConfigFileName)
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(w, "  config: %s\n", path)
		} else {
			fmt.Fprintf(w, "  config: %s (created on first run)\n", path)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if exec.Command("git", "-C", cwd, "rev-parse", "--git-dir").Run() != nil {
			fmt.Fprintf(w, "  note:   %s is not a git repository; run %s from inside one\n", cwd, binName)
		}
	}
	fmt.Fprintln(w)
	if failed == 0 {
		fmt.Fprintf(w, "Ready. Run `%s` inside a git repository.\n", binName)
	}
	return failed
}

func versionOf(tool string, args ...string) func() (string, bool) {
	return func() (string, bool) {
		if _, err := exec.LookPath(tool); err != nil {
			return "not installed", false
		}
		out, err := exec.Command(tool, args...).Output()
		if err != nil {
			return "installed, but `" + tool + " " + strings.Join(args, " ") + "` failed", false
		}
		return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), true
	}
}

// errUnverifiable means the program string runs through a shell in a way this
// cannot follow -- a variable, a substitution -- so whether it starts is left to
// tmux rather than refused here.
var errUnverifiable = errors.New("cannot be checked without running it")

var envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// programExecutable picks the word of a program string that names what runs.
// tmux hands the whole string to a shell, so "FOO=1 claude", "env FOO=1 claude"
// and "~/bin/aider" all start fine; looking up the first word as given refused
// every one of them, and with them the whole app.
func programExecutable(program string) (string, error) {
	fields := strings.Fields(program)
	i := 0
	for i < len(fields) && envAssignment.MatchString(fields[i]) {
		i++
	}
	if i < len(fields) && fields[i] == "env" {
		i++
		for i < len(fields) && (envAssignment.MatchString(fields[i]) || strings.HasPrefix(fields[i], "-")) {
			switch fields[i] {
			case "-u", "-C", "-S", "--unset", "--chdir", "--split-string":
				i++ // these take the next word as their value
			}
			i++
		}
	}
	if i >= len(fields) {
		return "", fmt.Errorf("no program configured")
	}
	name := fields[i]
	if strings.ContainsAny(name, "$`(){}|;&<>") {
		return name, errUnverifiable
	}
	if name == "~" || strings.HasPrefix(name, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, strings.TrimPrefix(name, "~"))
		}
	}
	return name, nil
}

// resolveProgram finds the executable a program string such as "claude" or
// "aider --model x" runs. For claude it also asks the login shell, which is how
// config.GetClaudeCommand finds an install that only an rc file puts on PATH.
func resolveProgram(program string) (string, error) {
	name, err := programExecutable(program)
	if err != nil {
		return name, err
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if filepath.Base(name) == "claude" {
		return config.GetClaudeCommand()
	}
	return "", fmt.Errorf("%s not found", name)
}

func programHint(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	switch filepath.Base(fields[0]) {
	case "claude":
		return "curl -fsSL https://claude.ai/install.sh | bash   (see https://docs.anthropic.com/en/docs/claude-code)"
	case "codex":
		return "npm install -g @openai/codex"
	case "gemini":
		return "npm install -g @google/gemini-cli"
	case "aider":
		return "python -m pip install aider-install && aider-install"
	}
	return fmt.Sprintf("install %s, or point `default_program` in the config at an agent you have", fields[0])
}

// pkgHint names the install command for this machine's package manager.
func pkgHint(brewName, linuxName string) string {
	if runtime.GOOS == "darwin" {
		return "brew install " + brewName
	}
	managers := []struct{ bin, cmd string }{
		{"apt-get", "sudo apt-get install -y "},
		{"dnf", "sudo dnf install -y "},
		{"pacman", "sudo pacman -S "},
		{"zypper", "sudo zypper install "},
		{"apk", "sudo apk add "},
		{"brew", "brew install "},
	}
	for _, m := range managers {
		if _, err := exec.LookPath(m.bin); err == nil {
			if linuxName == "gh" && m.bin == "apt-get" {
				// Debian/Ubuntu's own gh is years old; GitHub's apt repo is the documented route.
				return "see https://github.com/cli/cli/blob/trunk/docs/install_linux.md"
			}
			return m.cmd + linuxName
		}
	}
	return "install " + linuxName + " with your package manager"
}

// preflight stops the TUI from starting when a session could never launch,
// with the reason, instead of letting the first `n` time out.
func preflight(program string) error {
	var missing []string
	for _, tool := range []string{"git", "tmux"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if _, err := resolveProgram(program); err != nil && !errors.Is(err, errUnverifiable) {
		name, _ := programExecutable(program)
		if name == "" {
			name = program
		}
		missing = append(missing, fmt.Sprintf("%q (the agent program)", name))
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s cannot start sessions: %s not found.\nRun `%s doctor` for install commands",
		binName, strings.Join(missing, ", "), binName)
}

// legacyStateMarker records that the user chose to start fresh rather than
// carry their claude-squad sessions over, so the question is asked once.
const legacyStateMarker = ".claude-squad-not-migrated"

// checkLegacyState refuses to start on a machine that still has claude-squad
// sessions and no Adroit ones. Starting anyway writes a stub ~/.adroit, the
// session list reads empty although every session is still there, and the
// default branch prefix quietly replaces the configured one.
func checkLegacyState(skip bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	newDir, err := config.GetConfigDir()
	if err != nil {
		return nil
	}
	oldState := filepath.Join(home, ".claude-squad", config.StateFileName)
	if _, err := os.Stat(oldState); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(newDir, config.StateFileName)); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(newDir, legacyStateMarker)); err == nil {
		return nil
	}
	if skip {
		if err := os.MkdirAll(newDir, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(newDir, legacyStateMarker), nil, 0644)
	}
	return fmt.Errorf(`found claude-squad sessions in %s.

To carry them over to %s (config, sessions, worktrees, tmux sessions), quit
claude-squad and run:

  curl -fsSL https://raw.githubusercontent.com/AlexanderWeismannn/adroit/main/scripts/migrate-to-adroit.sh | bash -s -- --apply

To start fresh and leave claude-squad as it is, run once:

  %s --skip-claude-squad-migration`, filepath.Dir(oldState), newDir, binName)
}
