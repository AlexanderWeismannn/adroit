package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/secrets"

	"github.com/spf13/cobra"
)

// agentRunCmd is how a session's agent is started when its profile has keys:
// tmux runs `adroit agent-run --profile codex -- <command>`, which reads the
// profile's keys from the secrets store into the environment and then becomes
// the agent. The keys never appear in a command line -- tmux's, ps's, or the
// state file's -- and never in config.json.
var agentRunCmd = &cobra.Command{
	Use:                "agent-run --profile NAME -- COMMAND",
	Short:              "Start an agent with its profile's API keys (used internally)",
	Hidden:             true,
	DisableFlagParsing: false,
	SilenceUsage:       true,
	SilenceErrors:      true,
	Args:               cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile, _ := cmd.Flags().GetString("profile")
		env, missing := agentEnv(profile, secrets.Default())
		for _, name := range missing {
			// Said in the pane, where the user is looking: the agent is about to
			// fail to authenticate, and this is why.
			fmt.Fprintf(os.Stderr, "adroit: no %s stored for %q; set it in Settings (s)\n", name, profile)
		}
		return syscall.Exec("/bin/sh", []string{"sh", "-c", args[0]}, env)
	},
}

func init() {
	agentRunCmd.Flags().String("profile", "", "profile whose keys to load")
	rootCmd.AddCommand(agentRunCmd)
}

// agentEnv is this process's environment plus the profile's stored keys. A
// stored key wins over one already exported, since setting it in Settings is
// the more deliberate act; a key that is neither stored nor exported is
// reported missing.
func agentEnv(profileName string, store secrets.Store) (env []string, missing []string) {
	env = os.Environ()
	cfg := config.PeekConfig()
	var keys []string
	for _, p := range cfg.Profiles {
		if p.Name == profileName {
			keys = p.Keys
			break
		}
	}
	for _, name := range keys {
		v, err := store.Get(secrets.Key(profileName, name))
		switch {
		case err == nil && v != "":
			env = setEnv(env, name, v)
		case errors.Is(err, secrets.ErrNotFound) || err == nil:
			if os.Getenv(name) == "" {
				missing = append(missing, name)
			}
		default:
			fmt.Fprintf(os.Stderr, "adroit: could not read %s: %v\n", name, err)
		}
	}
	return env, missing
}

// setEnv replaces name in env rather than appending a second entry: getenv
// returns the first match, so an appended value would lose to an exported one.
func setEnv(env []string, name, value string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if len(kv) > len(name) && kv[:len(name)+1] == name+"=" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, name+"="+value)
}
