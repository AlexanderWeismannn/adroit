package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tmux runs the program string through a shell, so anything a shell would start
// has to get past the startup check. Looking up the first word as given refused
// "FOO=1 claude" and "~/bin/aider" -- and with them, the whole app.
func TestProgramExecutableFindsTheCommandWord(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	cases := []struct {
		program, want string
		unverifiable  bool
	}{
		{"claude", "claude", false},
		{"aider --model ollama_chat/gemma3:1b", "aider", false},
		{"FOO=1 claude", "claude", false},
		{"A=1 B=two codex --full-auto", "codex", false},
		{"env -u TMUX FOO=1 gemini", "gemini", false},
		{"~/bin/aider", filepath.Join(home, "bin/aider"), false},
		{"$HOME/bin/aider", "$HOME/bin/aider", true},
	}
	for _, c := range cases {
		got, err := programExecutable(c.program)
		if c.unverifiable != errors.Is(err, errUnverifiable) || (!c.unverifiable && err != nil) {
			t.Errorf("programExecutable(%q) err = %v, unverifiable want %v", c.program, err, c.unverifiable)
		}
		if got != c.want {
			t.Errorf("programExecutable(%q) = %q, want %q", c.program, got, c.want)
		}
	}
}

// And the check itself: an env prefix in front of a program that exists must
// not be reported missing.
func TestPreflightAcceptsAnEnvPrefix(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	binName = "adroit"
	if err := preflight("FOO=1 fake-agent --flag"); err != nil {
		t.Fatalf("preflight refused a program a shell would start: %v", err)
	}
	if err := preflight("no-such-agent-xyz"); err == nil {
		t.Fatal("preflight accepted a program that does not exist")
	}
}
