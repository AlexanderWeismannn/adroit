package config

import (
	"encoding/json"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/theme"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	ConfigFileName = "config.json"
	defaultProgram = "claude"
)

// GetConfigDir returns the path to the application's configuration directory
func GetConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config home directory: %w", err)
	}
	return filepath.Join(homeDir, ".adroit"), nil
}

// Profile represents a named program configuration
type Profile struct {
	Name    string `json:"name"`
	Program string `json:"program"`
}

// Config represents the application configuration
type Config struct {
	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// AutoYes is a flag to automatically accept all prompts.
	AutoYes bool `json:"auto_yes"`
	// DaemonPollInterval is the interval (ms) at which the daemon polls sessions for autoyes mode.
	DaemonPollInterval int `json:"daemon_poll_interval"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// Theme names a built-in colour palette. Empty or "default" keeps the one the
	// interface ships with; an unknown name warns and falls back to it, because a
	// typo in a decoration is no reason to refuse to start.
	Theme string `json:"theme,omitempty"`
	// Colors overrides individual roles on top of Theme. A value is either a bare
	// colour used for both terminal backgrounds, or {"light": …, "dark": …}.
	Colors map[string]theme.Pair `json:"colors,omitempty"`
	// PreserveBranchCase keeps the case of a new session's branch name instead of
	// lower-casing it. Off by default: two sessions differing only in case would
	// produce two branches a case-insensitive filesystem cannot hold at once, since
	// git stores refs as files.
	PreserveBranchCase bool `json:"preserve_branch_case,omitempty"`
	// GitHubCIStatus controls the CI status badge shown next to each branch in
	// the instance list. Nil means enabled: the badge is opt-out, and every config
	// written before the feature existed has no such key, so nil must not read as
	// false or the feature would be invisible on upgrade.
	GitHubCIStatus *bool `json:"github_ci_status,omitempty"`
	// UpstreamStatus controls the badge saying a session's branch is behind or
	// diverged from its remote counterpart, and the key that updates it. Nil
	// means enabled, for the same upgrade reason as GitHubCIStatus. Switch it off
	// on a metered or offline connection: it is the one feature here that fetches
	// from the network on a timer.
	UpstreamStatus *bool `json:"upstream_status,omitempty"`
	// SyncBaseBranch fast-forwards the repository's default branch to its
	// upstream before a new session branches off it, so a session started on a
	// stale master does not begin its life behind. Nil means enabled, for the
	// same upgrade reason as GitHubCIStatus. Switch it off on a metered or
	// offline connection, or where the default branch is deliberately held back.
	SyncBaseBranch *bool `json:"sync_base_branch,omitempty"`
	// Bell rings the terminal bell when a session finishes its turn or stops on a
	// question for you. Nil means enabled, for the same upgrade reason as
	// GitHubCIStatus. Inside tmux the bell reaches the outer terminal, which is
	// what makes Windows Terminal flash the taskbar of an unfocused window.
	Bell *bool `json:"bell,omitempty"`
	// NotifyCommand, when set, is run through `sh -c` on the same events, for a
	// desktop notification the bell cannot give. It gets ADROIT_SESSION (the
	// session's title) and ADROIT_EVENT ("finished" or "needs-input") in its
	// environment. Its output is discarded and it is not waited on.
	NotifyCommand string `json:"notify_command,omitempty"`
	// Dev describes the development stack a session can run, for any repository
	// with no entry of its own in Repos. Nil disables the feature: there is
	// no sensible default command for an arbitrary repository, and guessing one
	// would run something unexpected in the worktree.
	//
	// With more than one repository in play this is usually the wrong place to
	// put a stack: `npm run dev` is an answer about one project, and running it
	// in another repository's worktree is how you get a stack that fails in ways
	// that have nothing to do with the code in front of you.
	Dev *DevConfig `json:"dev,omitempty"`
	// Repos holds the settings that are answers about ONE repository rather than
	// about you, keyed by the path of the repository's root -- the main checkout,
	// not a worktree, since every session of a repository shares them. A leading
	// `~` is expanded and the path is cleaned, so the spelling you would type is
	// the spelling that matches.
	Repos map[string]*RepoConfig `json:"repos,omitempty"`
}

// RepoConfig overrides, for one repository, the settings that are properties of
// the project rather than of the person running it.
//
// Every field is a pointer because absent and empty mean different things here:
// no `branch_prefix` key inherits the global one, and `"branch_prefix": ""` is
// an explicit "no prefix in this repository", which is unreachable otherwise.
type RepoConfig struct {
	// BranchPrefix replaces the global prefix for branches cut in this
	// repository. A ticket convention is a property of the project -- "TASK-" is
	// the right answer in one repository and noise in every other.
	BranchPrefix *string `json:"branch_prefix,omitempty"`
	// PreserveBranchCase replaces the global case policy for this repository.
	PreserveBranchCase *bool `json:"preserve_branch_case,omitempty"`
	// Dev is the development stack that runs this repository.
	Dev *DevConfig `json:"dev,omitempty"`
}

// DevConfig describes a long-running development stack -- servers, watchers, a
// queue worker -- that one session at a time can own.
//
// The stack is a SINGLETON pointed at a session, not one stack per session: the
// ports it binds and the services it talks to are machine-wide, so two sessions
// running it at once would collide on the first listen. Pointing it somewhere
// else tears the old one down.
type DevConfig struct {
	// Command is the shell command that runs the whole stack, from the worktree
	// root. One command, not a list: the pane it runs in is captured whole for
	// display, and tmux capture-pane reads a single pane, so a stack split across
	// panes would show only a fraction of itself. Multiplex inside the command.
	Command string `json:"command"`
	// Env is added to the inherited environment for Command.
	Env map[string]string `json:"env,omitempty"`
	// Checks are the readiness lamps, polled together until all pass.
	Checks []DevCheck `json:"checks,omitempty"`
	// OpenURL is opened once every check passes. Empty opens nothing.
	OpenURL string `json:"open_url,omitempty"`
	// OpenCommand opens OpenURL, which is appended as the final argument. Empty
	// picks a platform default.
	OpenCommand []string `json:"open_command,omitempty"`
	// ReadyTimeoutSeconds bounds the wait for the checks. Zero means 180.
	ReadyTimeoutSeconds int `json:"ready_timeout_seconds,omitempty"`
}

// DevCheck is one readiness lamp.
type DevCheck struct {
	// Name labels the lamp.
	Name string `json:"name"`
	// Type is "tcp" or "http".
	Type string `json:"type"`
	// Target is host:port for tcp, a URL for http.
	Target string `json:"target"`
	// ExpectStatus, for http, is the status code that counts as healthy. Zero
	// accepts any response at all, which is the right bar for "is it listening":
	// a 401 or a 404 proves a server is answering as well as a 200 does.
	ExpectStatus int `json:"expect_status,omitempty"`
	// OwnCwd requires the process holding the port to be running inside the
	// session's worktree. Without it a server left over from ANOTHER worktree
	// answers on the same port and every lamp goes green while the code being
	// served is the wrong branch -- the exact confusion switching exists to end.
	// Only meaningful for a port this stack owns; never for a shared service.
	OwnCwd bool `json:"own_cwd,omitempty"`
	// StartCommand is run once, before polling, when the check is already failing
	// as the stack starts. For a shared service the stack needs but does not own,
	// such as a system Redis.
	StartCommand string `json:"start_command,omitempty"`
}

// DefaultReadyTimeout is how long the stack has to bring every check up.
const DefaultReadyTimeout = 180

// ReadyTimeoutSecs resolves the configured timeout, applying the default.
func (d *DevConfig) ReadyTimeoutSecs() int {
	if d == nil || d.ReadyTimeoutSeconds <= 0 {
		return DefaultReadyTimeout
	}
	return d.ReadyTimeoutSeconds
}

// CIStatusEnabled reports whether the GitHub CI status badge should be shown.
func (c *Config) CIStatusEnabled() bool {
	return c.GitHubCIStatus == nil || *c.GitHubCIStatus
}

// UpstreamStatusEnabled reports whether a session's standing against its remote
// branch should be tracked and shown.
func (c *Config) UpstreamStatusEnabled() bool {
	return c.UpstreamStatus == nil || *c.UpstreamStatus
}

// BellEnabled reports whether a session needing attention rings the bell.
func (c *Config) BellEnabled() bool {
	return c.Bell == nil || *c.Bell
}

// SyncBaseBranchEnabled reports whether the default branch should be brought up
// to date before a new session's branch is cut from it.
func (c *Config) SyncBaseBranchEnabled() bool {
	return c.SyncBaseBranch == nil || *c.SyncBaseBranch
}

// DevFor returns the stack definition that runs repoPath, or nil if none does.
//
// An entry in DevByRepo wins; Dev is the fallback for a repository with none.
// An entry that exists but has no command is an explicit "no stack here", and
// does NOT fall back -- otherwise the only way to say "don't run the global
// stack in this repo" would be to have no global stack at all.
func (c *Config) DevFor(repoPath string) *DevConfig {
	if c == nil {
		return nil
	}
	if entry := c.repoEntry(repoPath); entry != nil && entry.Dev != nil {
		return entry.Dev
	}
	return c.Dev
}

// AnyDev reports whether a stack is defined for any repository at all. It is
// what decides whether the feature is worth mentioning in the interface, as
// opposed to whether it can run here, which is DevFor.
func (c *Config) AnyDev() bool {
	if c == nil {
		return false
	}
	if c.Dev != nil && strings.TrimSpace(c.Dev.Command) != "" {
		return true
	}
	for _, v := range c.Repos {
		if v != nil && v.Dev != nil && strings.TrimSpace(v.Dev.Command) != "" {
			return true
		}
	}
	return false
}

// repoEntry returns the configuration for a repository, or nil if it has none.
func (c *Config) repoEntry(repoPath string) *RepoConfig {
	key := repoKey(repoPath)
	if c == nil || key == "" {
		return nil
	}
	for k, v := range c.Repos {
		if repoKey(k) == key {
			return v
		}
	}
	return nil
}

// BranchPrefixFor is the prefix for a branch cut in repoPath.
func (c *Config) BranchPrefixFor(repoPath string) string {
	if c == nil {
		return ""
	}
	if entry := c.repoEntry(repoPath); entry != nil && entry.BranchPrefix != nil {
		return *entry.BranchPrefix
	}
	return c.BranchPrefix
}

// PreserveBranchCaseFor is the case policy for a branch cut in repoPath.
func (c *Config) PreserveBranchCaseFor(repoPath string) bool {
	if c == nil {
		return false
	}
	if entry := c.repoEntry(repoPath); entry != nil && entry.PreserveBranchCase != nil {
		return *entry.PreserveBranchCase
	}
	return c.PreserveBranchCase
}

// repoKey normalises a repository path for comparison: `~` is expanded,
// separators are cleaned up and a trailing one is dropped. Compared verbatim
// after that -- not through EvalSymlinks, which would make the answer depend on
// whether the repository happens to be reachable right now.
func repoKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	// Resolved, because git reports the real path of a repository root and the
	// config holds whatever the user typed: a key under a symlinked directory
	// (macOS's /var -> /private/var, a home on another volume) would otherwise
	// never match, and the repository silently gets the global settings.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// GetProgram returns the program to run. If Profiles is non-empty and
// DefaultProgram matches a profile name, that profile's Program is returned.
// Otherwise DefaultProgram is used.
func (c *Config) GetProgram() string {
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			return rehomeProgram(p.Program)
		}
	}
	return rehomeProgram(c.DefaultProgram)
}

// rehomeProgram repairs a command whose absolute path no longer exists.
//
// The config is written with the agent's path RESOLVED -- which it has to be,
// since `claude` is often an install that only a login shell can find -- and an
// absolute path under one machine's home directory is the one thing in this file
// that cannot survive being copied to another. The session then fails in the
// pane with "no such file or directory" and nothing says which file.
//
// Only an absolute first word that is missing is touched, so a command that
// works is never second-guessed, and any arguments after it are kept.
func rehomeProgram(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 || !filepath.IsAbs(fields[0]) {
		return program
	}
	if _, err := os.Stat(fields[0]); err == nil {
		return program
	}

	name := filepath.Base(fields[0])
	found, err := exec.LookPath(name)
	if err != nil {
		// Not on PATH either. A login shell may still know it -- that is the case
		// GetClaudeCommand exists for -- but only for claude, and only there.
		if name == defaultProgram {
			if resolved, cmdErr := GetClaudeCommand(); cmdErr == nil {
				found = resolved
			}
		}
		if found == "" {
			log.WarningLog.Printf("configured program %q does not exist and %q is not on PATH", fields[0], name)
			return program
		}
	}

	log.InfoLog.Printf("configured program %q is missing; using %q", fields[0], found)
	return strings.Join(append([]string{found}, fields[1:]...), " ")
}

// GetProfiles returns a unified list of profiles. If Profiles is defined,
// those are returned with the default profile first. Otherwise, a single
// profile is synthesized from DefaultProgram.
//
// A picked profile's Program is launched directly, without passing through
// GetProgram, so the same repair is applied here -- otherwise a config moved
// between machines would work for the default agent and fail for every other.
// Only Program: Name is what the picker displays and what the config keys on.
func (c *Config) GetProfiles() []Profile {
	if len(c.Profiles) == 0 {
		return []Profile{{Name: c.DefaultProgram, Program: rehomeProgram(c.DefaultProgram)}}
	}
	// Reorder so the default profile comes first.
	profiles := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			p.Program = rehomeProgram(p.Program)
			profiles = append(profiles, p)
			break
		}
	}
	for _, p := range c.Profiles {
		if p.Name != c.DefaultProgram {
			p.Program = rehomeProgram(p.Program)
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	program, err := GetClaudeCommand()
	if err != nil {
		log.ErrorLog.Printf("failed to get claude command: %v", err)
		program = defaultProgram
	}

	return &Config{
		DefaultProgram:     program,
		AutoYes:            false,
		DaemonPollInterval: 1000,
		GitHubCIStatus:     boolPtr(true),
		Theme:              "default",
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.ErrorLog.Printf("failed to get current user: %v", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
	}
}

func boolPtr(b bool) *bool { return &b }

// GetClaudeCommand attempts to find the "claude" command in the user's shell
// It checks in the following order:
// 1. Shell alias resolution: using "which" command
// 2. PATH lookup
//
// If both fail, it returns an error.
func GetClaudeCommand() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash" // Default to bash if SHELL is not set
	}

	// Force the shell to load the user's profile and then run the command
	// For zsh, source .zshrc; for bash, source .bashrc
	var shellCmd string
	if strings.Contains(shell, "zsh") {
		shellCmd = "source ~/.zshrc &>/dev/null || true; which claude"
	} else if strings.Contains(shell, "bash") {
		// .bash_profile too: on macOS a login shell reads that and never .bashrc,
		// so an install that only .bash_profile puts on PATH was not found.
		shellCmd = "source ~/.bash_profile &>/dev/null || true; source ~/.bashrc &>/dev/null || true; which claude"
	} else {
		shellCmd = "which claude"
	}

	cmd := exec.Command(shell, "-c", shellCmd)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		path := strings.TrimSpace(string(output))
		if path != "" {
			// Check if the output is an alias definition and extract the actual path
			// Handle formats like "claude: aliased to /path/to/claude" or other shell-specific formats
			aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)
			matches := aliasRegex.FindStringSubmatch(path)
			if len(matches) > 1 {
				path = matches[1]
			}
			return path, nil
		}
	}

	// Otherwise, try to find in PATH directly
	claudePath, err := exec.LookPath("claude")
	if err == nil {
		return claudePath, nil
	}

	return "", fmt.Errorf("claude command not found in aliases or PATH")
}

func LoadConfig() *Config { return loadConfig(true) }

// PeekConfig reads the config like LoadConfig but never writes one. For
// commands that only report: `adroit doctor` used to create ~/.adroit with a
// default config -- branch prefix and all -- on a machine where Adroit had
// never run, and the installer runs doctor.
func PeekConfig() *Config { return loadConfig(false) }

func loadConfig(save bool) *Config {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultConfig()
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default config if file doesn't exist
			defaultCfg := DefaultConfig()
			if save {
				if saveErr := saveConfig(defaultCfg); saveErr != nil {
					log.WarningLog.Printf("failed to save default config: %v", saveErr)
				}
			}
			return defaultCfg
		}

		log.WarningLog.Printf("failed to get config file: %v", err)
		return DefaultConfig()
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.ErrorLog.Printf("failed to parse config file: %v", err)
		// Defaults are a different branch prefix, no dev stack and the default
		// theme -- worth saying on screen, or new branches quietly come out
		// named wrong.
		noteLoadWarning(fmt.Sprintf("config.json could not be parsed, so the defaults are in use: %v", err))
		return DefaultConfig()
	}

	return &config
}

// saveConfig saves the configuration to disk
func saveConfig(config *Config) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// SaveConfig exports the saveConfig function for use by other packages
func SaveConfig(config *Config) error {
	return saveConfig(config)
}

var (
	loadWarningsMu sync.Mutex
	loadWarnings   []string
)

// noteLoadWarning records a problem found loading the config or the state, for
// the interface to show once it is up: by then the log is the only other place
// it would appear. A repeat is dropped, since both are loaded more than once.
func noteLoadWarning(warning string) {
	loadWarningsMu.Lock()
	defer loadWarningsMu.Unlock()
	for _, w := range loadWarnings {
		if w == warning {
			return
		}
	}
	loadWarnings = append(loadWarnings, warning)
}

// TakeLoadWarnings returns the recorded load problems and clears them.
func TakeLoadWarnings() []string {
	loadWarningsMu.Lock()
	defer loadWarningsMu.Unlock()
	taken := loadWarnings
	loadWarnings = nil
	return taken
}
