package main

import (
	"github.com/AlexanderWeismannn/adroit/theme"

	"context"
	"encoding/json"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/app"
	cmd2 "github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/daemon"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

var (
	// version is stamped by the release build (-ldflags "-X main.version=...");
	// versionString falls back to what `go install` records.
	version        = ""
	programFlag    string
	autoYesFlag    bool
	daemonFlag     bool
	skipLegacyFlag bool
	binName        string
	rootCmd        = &cobra.Command{
		Use:   "adroit",
		Short: "Adroit - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		// A missing tool or a wrong directory is not a usage error; the usage block
		// buried the one line that said what to do. main prints the error.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log.Initialize(daemonFlag)
			defer log.Close()

			if daemonFlag {
				cfg := config.LoadConfig()
				err := daemon.RunDaemon(cfg)
				log.ErrorLog.Printf("failed to start daemon %v", err)
				return err
			}

			// Check if we're in a git repository
			currentDir, err := filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			if !git.IsGitRepo(currentDir) {
				return fmt.Errorf("error: %s must be run from within a git repository", binName)
			}

			// Before the lock: taking it creates the config directory.
			if err := checkLegacyState(skipLegacyFlag); err != nil {
				return err
			}

			// One interactive Adroit at a time. Taken before anything is loaded or
			// rendered: a second one that got as far as drawing would already have
			// opened a tmux client against every session.
			lock, holder, err := config.AcquireInstanceLock()
			if err != nil {
				return err
			}
			if lock == nil {
				return fmt.Errorf(
					"%s is already running (%s).\n\n"+
						"Closing a terminal window does not stop it -- tmux keeps it alive -- so go back to\n"+
						"that terminal rather than starting a second one. If it is wedged, `kill %d` and retry",
					binName, config.DescribeLockHolder(holder), holder)
			}
			defer func() {
				if err := lock.Release(); err != nil {
					log.WarningLog.Printf("could not release the instance lock: %v", err)
				}
			}()

			cfg := config.LoadConfig()

			// Install the palette before anything renders. Warnings are logged
			// rather than fatal: a theme is decoration, and refusing to start over a
			// misspelled colour would be wildly out of proportion -- but they must
			// be recorded, or a misspelled role silently does nothing at all.
			palette, warnings := theme.Resolve(cfg.Theme, cfg.Colors)
			for _, warning := range warnings {
				log.WarningLog.Printf("theme: %v", warning)
			}
			theme.Set(palette)

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}
			// AutoYes flag overrides config
			autoYes := cfg.AutoYes
			if autoYesFlag {
				autoYes = true
			}
			if err := preflight(program); err != nil {
				return err
			}
			if autoYes {
				defer func() {
					if err := daemon.LaunchDaemon(); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}

			return app.Run(ctx, program, autoYes)
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			if err := storage.DeleteAllInstances(); err != nil {
				return fmt.Errorf("failed to reset storage: %w", err)
			}
			fmt.Println("Storage has been reset successfully")

			if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
				return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
			}
			fmt.Println("Tmux sessions have been cleaned up")

			if err := git.CleanupWorktrees(); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				return err
			}
			fmt.Println("daemon has been stopped")

			return nil
		},
	}

	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Print debug information like config paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			cfg := config.LoadConfig()

			configDir, err := config.GetConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get config directory: %w", err)
			}
			configJson, _ := json.MarshalIndent(cfg, "", "  ")

			fmt.Printf("Config: %s\n%s\n", filepath.Join(configDir, config.ConfigFileName), configJson)

			return nil
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s\n", binName, versionString())
			fmt.Println("a fork of claude-squad -- https://github.com/smtg-ai/claude-squad")
		},
	}
)

func init() {
	// Persistent so `adroit doctor -p codex` checks the program it names.
	rootCmd.PersistentFlags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")

	rootCmd.Flags().BoolVar(&skipLegacyFlag, "skip-claude-squad-migration", false,
		"Start fresh instead of migrating existing claude-squad sessions")

	// Hide the daemonFlag as it's only for internal use
	err := rootCmd.Flags().MarkHidden("daemon")
	if err != nil {
		panic(err)
	}

	rootCmd.AddCommand(themeCmd)
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
}

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func main() {
	// Extract the binary name from how this was invoked
	binName = filepath.Base(os.Args[0])
	rootCmd.Use = binName

	if err := rootCmd.Execute(); err != nil {
		// Errors belong on stderr, and a command that failed must not report
		// success: `cs theme use typo && ...` would otherwise run the second half.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
