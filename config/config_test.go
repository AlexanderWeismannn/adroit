package config

import (
	"github.com/AlexanderWeismannn/adroit/log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestGetClaudeCommand(t *testing.T) {
	originalShell := os.Getenv("SHELL")
	originalPath := os.Getenv("PATH")
	defer func() {
		os.Setenv("SHELL", originalShell)
		os.Setenv("PATH", originalPath)
	}()

	t.Run("finds claude in PATH", func(t *testing.T) {
		// Create a temporary directory with a mock claude executable
		tempDir := t.TempDir()
		claudePath := filepath.Join(tempDir, "claude")

		// Create a mock executable
		err := os.WriteFile(claudePath, []byte("#!/bin/bash\necho 'mock claude'"), 0755)
		require.NoError(t, err)

		// Set PATH to include our temp directory
		os.Setenv("PATH", tempDir+":"+originalPath)
		os.Setenv("SHELL", "/bin/bash")

		result, err := GetClaudeCommand()

		assert.NoError(t, err)
		assert.True(t, strings.Contains(result, "claude"))
	})

	t.Run("handles missing claude command", func(t *testing.T) {
		// Set PATH to a directory that doesn't contain claude
		tempDir := t.TempDir()
		os.Setenv("PATH", tempDir)
		os.Setenv("SHELL", "/bin/bash")

		result, err := GetClaudeCommand()

		assert.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "claude command not found")
	})

	t.Run("handles empty SHELL environment", func(t *testing.T) {
		// Create a temporary directory with a mock claude executable
		tempDir := t.TempDir()
		claudePath := filepath.Join(tempDir, "claude")

		// Create a mock executable
		err := os.WriteFile(claudePath, []byte("#!/bin/bash\necho 'mock claude'"), 0755)
		require.NoError(t, err)

		// Set PATH and unset SHELL
		os.Setenv("PATH", tempDir+":"+originalPath)
		os.Unsetenv("SHELL")

		result, err := GetClaudeCommand()

		assert.NoError(t, err)
		assert.True(t, strings.Contains(result, "claude"))
	})

	t.Run("handles alias parsing", func(t *testing.T) {
		// Test core alias formats
		aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)

		// Standard alias format
		output := "claude: aliased to /usr/local/bin/claude"
		matches := aliasRegex.FindStringSubmatch(output)
		assert.Len(t, matches, 2)
		assert.Equal(t, "/usr/local/bin/claude", matches[1])

		// Direct path (no alias)
		output = "/usr/local/bin/claude"
		matches = aliasRegex.FindStringSubmatch(output)
		assert.Len(t, matches, 0)
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("creates config with default values", func(t *testing.T) {
		config := DefaultConfig()

		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.False(t, config.AutoYes)
		assert.Equal(t, 1000, config.DaemonPollInterval)
		assert.NotEmpty(t, config.BranchPrefix)
		assert.True(t, strings.HasSuffix(config.BranchPrefix, "/"))
	})

}

func TestGetConfigDir(t *testing.T) {
	t.Run("returns valid config directory", func(t *testing.T) {
		configDir, err := GetConfigDir()

		assert.NoError(t, err)
		assert.NotEmpty(t, configDir)
		assert.True(t, strings.HasSuffix(configDir, ".adroit"))

		// Verify it's an absolute path
		assert.True(t, filepath.IsAbs(configDir))
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("returns default config when file doesn't exist", func(t *testing.T) {
		// Use a temporary home directory to avoid interfering with real config
		originalHome := os.Getenv("HOME")
		tempHome := t.TempDir()
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.False(t, config.AutoYes)
		assert.Equal(t, 1000, config.DaemonPollInterval)
		assert.NotEmpty(t, config.BranchPrefix)
	})

	t.Run("loads valid config file", func(t *testing.T) {
		// Create a temporary config directory
		tempHome := t.TempDir()
		configDir := filepath.Join(tempHome, ".adroit")
		err := os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		// Create a test config file
		configPath := filepath.Join(configDir, ConfigFileName)
		configContent := `{
			"default_program": "test-claude",
			"auto_yes": true,
			"daemon_poll_interval": 2000,
			"branch_prefix": "test/"
		}`
		err = os.WriteFile(configPath, []byte(configContent), 0644)
		require.NoError(t, err)

		// Override HOME environment
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		assert.NotNil(t, config)
		assert.Equal(t, "test-claude", config.DefaultProgram)
		assert.True(t, config.AutoYes)
		assert.Equal(t, 2000, config.DaemonPollInterval)
		assert.Equal(t, "test/", config.BranchPrefix)
	})

	t.Run("returns default config on invalid JSON", func(t *testing.T) {
		// Create a temporary config directory
		tempHome := t.TempDir()
		configDir := filepath.Join(tempHome, ".adroit")
		err := os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		// Create an invalid config file
		configPath := filepath.Join(configDir, ConfigFileName)
		invalidContent := `{"invalid": json content}`
		err = os.WriteFile(configPath, []byte(invalidContent), 0644)
		require.NoError(t, err)

		// Override HOME environment
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		config := LoadConfig()

		// Should return default config when JSON is invalid
		assert.NotNil(t, config)
		assert.NotEmpty(t, config.DefaultProgram)
		assert.False(t, config.AutoYes)                  // Default value
		assert.Equal(t, 1000, config.DaemonPollInterval) // Default value
	})
}

func TestGetProgram(t *testing.T) {
	t.Run("no profiles returns default_program as-is", func(t *testing.T) {
		cfg := &Config{DefaultProgram: "/opt/agents/demo-agent"}
		assert.Equal(t, "/opt/agents/demo-agent", cfg.GetProgram())
	})

	t.Run("profiles defined and default_program matches a profile name", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "claude",
			Profiles: []Profile{
				{Name: "claude", Program: "/opt/agents/demo-agent"},
				{Name: "aider", Program: "aider --model ollama_chat/gemma3:1b"},
			},
		}
		assert.Equal(t, "/opt/agents/demo-agent", cfg.GetProgram())
	})

	t.Run("profiles defined but default_program does not match any profile", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "some-other-program",
			Profiles: []Profile{
				{Name: "claude", Program: "/opt/agents/demo-agent"},
			},
		}
		assert.Equal(t, "some-other-program", cfg.GetProgram())
	})
}

func TestGetProfiles(t *testing.T) {
	t.Run("no profiles returns single synthetic profile", func(t *testing.T) {
		cfg := &Config{DefaultProgram: "/opt/agents/demo-agent"}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 1)
		assert.Equal(t, "/opt/agents/demo-agent", profiles[0].Name)
		assert.Equal(t, "/opt/agents/demo-agent", profiles[0].Program)
	})

	t.Run("profiles defined returns them with default first", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "aider",
			Profiles: []Profile{
				{Name: "claude", Program: "/opt/agents/demo-agent"},
				{Name: "aider", Program: "aider --model gemma"},
			},
		}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 2)
		assert.Equal(t, "aider", profiles[0].Name)
		assert.Equal(t, "claude", profiles[1].Name)
	})

	t.Run("profiles defined but default not matching preserves order", func(t *testing.T) {
		cfg := &Config{
			DefaultProgram: "other",
			Profiles: []Profile{
				{Name: "claude", Program: "/opt/agents/demo-agent"},
				{Name: "aider", Program: "aider --model gemma"},
			},
		}
		profiles := cfg.GetProfiles()
		assert.Len(t, profiles, 2)
		assert.Equal(t, "claude", profiles[0].Name)
		assert.Equal(t, "aider", profiles[1].Name)
	})
}

func TestSaveConfig(t *testing.T) {
	t.Run("saves config to file", func(t *testing.T) {
		// Create a temporary config directory
		tempHome := t.TempDir()

		// Override HOME environment
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		defer os.Setenv("HOME", originalHome)

		// Create a test config
		testConfig := &Config{
			DefaultProgram:     "test-program",
			AutoYes:            true,
			DaemonPollInterval: 3000,
			BranchPrefix:       "test-branch/",
		}

		err := SaveConfig(testConfig)
		assert.NoError(t, err)

		// Verify the file was created
		configDir := filepath.Join(tempHome, ".adroit")
		configPath := filepath.Join(configDir, ConfigFileName)

		assert.FileExists(t, configPath)

		// Load and verify the content
		loadedConfig := LoadConfig()
		assert.Equal(t, testConfig.DefaultProgram, loadedConfig.DefaultProgram)
		assert.Equal(t, testConfig.AutoYes, loadedConfig.AutoYes)
		assert.Equal(t, testConfig.DaemonPollInterval, loadedConfig.DaemonPollInterval)
		assert.Equal(t, testConfig.BranchPrefix, loadedConfig.BranchPrefix)
	})
}

// One Adroit drives sessions from every repository you have open, so "which
// stack" is a question about the repository under the cursor, not about the
// process. Without this, `d` in any repository ran whichever project happened to
// own the single global `dev` block.
func TestDevForPicksTheRepositorysOwnStack(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	cfg := &Config{
		Dev: &DevConfig{Command: "global"},
		Repos: map[string]*RepoConfig{
			"~/myapp":        {Dev: &DevConfig{Command: "npm run dev"}},
			"/srv/other-app": {Dev: &DevConfig{Command: "cargo watch -x run"}},
			"/srv/no-stack":  {Dev: &DevConfig{Command: ""}},
		},
	}

	// A key written through a symlink, looked up by the real path -- which is what
	// git reports for a repository root. macOS puts every temp directory behind
	// one (/var -> /private/var), and a home on another volume is another.
	real := filepath.Join(t.TempDir(), "real-app")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-app")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink: %v", err)
	}
	cfg.Repos[link] = &RepoConfig{Dev: &DevConfig{Command: "make dev"}}

	tests := []struct {
		name string
		repo string
		want string
	}{
		{"an entry of its own wins", filepath.Join(home, "myapp"), "npm run dev"},
		{"a tilde key matches the expanded path", filepath.Join(home, "myapp") + "/", "npm run dev"},
		{"an untidy path is cleaned before matching", "/srv/other-app/../other-app", "cargo watch -x run"},
		{"a repository with no entry falls back", "/srv/unknown", "global"},
		{"a key under a symlink matches the real path", real, "make dev"},
		// Otherwise the only way to say "not here" would be to have no global
		// stack at all, which takes it away from every other repository too.
		{"an entry with no command is an explicit no", "/srv/no-stack", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.DevFor(tc.repo)
			if got == nil {
				t.Fatalf("DevFor(%q) = nil, want command %q", tc.repo, tc.want)
			}
			if got.Command != tc.want {
				t.Fatalf("DevFor(%q).Command = %q, want %q", tc.repo, got.Command, tc.want)
			}
		})
	}

	if got := (&Config{}).DevFor("/srv/anything"); got != nil {
		t.Fatalf("DevFor with nothing configured = %+v, want nil", got)
	}
}

// AnyDev decides whether the feature is worth mentioning at all, which is a
// different question from whether it can run in the repository under the cursor.
func TestAnyDevSeesAPerRepoStackWithNoGlobalOne(t *testing.T) {
	if (&Config{}).AnyDev() {
		t.Fatal("an empty config has no stack")
	}
	if (&Config{Dev: &DevConfig{Command: "   "}}).AnyDev() {
		t.Fatal("a blank command is not a stack")
	}
	onlyPerRepo := &Config{Repos: map[string]*RepoConfig{"/srv/app": {Dev: &DevConfig{Command: "make dev"}}}}
	if !onlyPerRepo.AnyDev() {
		t.Fatal("a per-repo stack with no global one still counts")
	}
}

// A ticket convention is a property of the project. "TASK-" is the right prefix
// in one repository and noise in every other, and one global prefix stamped it
// on every branch the tool ever cut.
func TestBranchNamingIsPerRepository(t *testing.T) {
	none := ""
	yes, no := true, false

	cfg := &Config{
		BranchPrefix:       "developer/",
		PreserveBranchCase: false,
		Repos: map[string]*RepoConfig{
			"/srv/ticketed": {BranchPrefix: strPtr("TASK-"), PreserveBranchCase: &yes},
			// Reachable only because the field is a pointer: an empty string in a
			// plain string field is indistinguishable from an absent key.
			"/srv/bare":  {BranchPrefix: &none},
			"/srv/cased": {PreserveBranchCase: &no},
		},
	}

	tests := []struct {
		repo         string
		wantPrefix   string
		wantPreserve bool
	}{
		{"/srv/ticketed", "TASK-", true},
		{"/srv/bare", "", false},
		{"/srv/cased", "developer/", false},
		{"/srv/unlisted", "developer/", false},
	}
	for _, tc := range tests {
		if got := cfg.BranchPrefixFor(tc.repo); got != tc.wantPrefix {
			t.Errorf("BranchPrefixFor(%q) = %q, want %q", tc.repo, got, tc.wantPrefix)
		}
		if got := cfg.PreserveBranchCaseFor(tc.repo); got != tc.wantPreserve {
			t.Errorf("PreserveBranchCaseFor(%q) = %v, want %v", tc.repo, got, tc.wantPreserve)
		}
	}
}

func strPtr(s string) *string { return &s }

// The config records the agent's path RESOLVED, because `claude` is often an
// install only a login shell can find. That absolute path under one machine's
// home directory is the one thing here that cannot survive being copied to
// another, and the session failed in the pane with "no such file or directory".
func TestGetProgramRepairsAPathFromAnotherMachine(t *testing.T) {
	real, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	gone := filepath.Join("/home", "someone-else", ".local", "bin", "sh")
	got := (&Config{DefaultProgram: gone}).GetProgram()
	if got != real {
		t.Fatalf("GetProgram() = %q, want it re-resolved to %q", got, real)
	}

	// Arguments belong to the command, not to the path, and survive the repair.
	got = (&Config{DefaultProgram: gone + " -c true"}).GetProgram()
	if got != real+" -c true" {
		t.Fatalf("GetProgram() = %q, want the arguments kept", got)
	}

	// A command that works is never second-guessed, and a bare name is left for
	// the shell to resolve as it always was.
	if got := (&Config{DefaultProgram: real}).GetProgram(); got != real {
		t.Fatalf("GetProgram() = %q, want the existing path untouched", got)
	}
	if got := (&Config{DefaultProgram: "some-agent --flag"}).GetProgram(); got != "some-agent --flag" {
		t.Fatalf("GetProgram() = %q, want a relative command left alone", got)
	}

	// Nothing to fall back to: better the original path in the error than a
	// silent substitution of something else.
	missing := "/nowhere/definitely-not-a-real-program-xyz"
	if got := (&Config{DefaultProgram: missing}).GetProgram(); got != missing {
		t.Fatalf("GetProgram() = %q, want the unresolvable path returned as-is", got)
	}
}
