package cmd

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A failure has to say what failed and why, and still expose the exit code to a
// caller that branches on it ("no server running" is exit 1 from tmux ls).
func TestExecErrorCarriesStderrAndExitCode(t *testing.T) {
	_, err := Exec{}.Output(exec.Command("sh", "-c", "echo out; echo can\\'t find session: x >&2; exit 3"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "can't find session: x") {
		t.Fatalf("error %q does not carry stderr", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("exit code lost: %v", err)
	}
	if !IsNoSuchSession(err) {
		t.Fatal("a missing session should be recognised")
	}

	out, err := Exec{}.Output(exec.Command("sh", "-c", "printf ok"))
	if err != nil || string(out) != "ok" {
		t.Fatalf("Output() = %q, %v", out, err)
	}
}

// A git killed by its timeout can leave ssh or git-remote-https behind holding
// its output pipe, and Wait used to go on until they let go of it: a one-minute
// fetch timeout became however long the network took, and the cache entry stayed
// "fetching" all that while. The helpers bound the wait for the pipe as well.
func TestATimeoutAlsoBoundsTheWaitForWhatTheCommandLeftBehind(t *testing.T) {
	for name, wrap := range map[string]func(*exec.Cmd) *exec.Cmd{
		"NonInteractiveGit": NonInteractiveGit,
		"Detach":            Detach,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			// The child holds stdout open for 20s after the shell is killed.
			c := wrap(exec.CommandContext(ctx, "sh", "-c", "sleep 20 & wait"))
			began := time.Now()
			_, _ = c.Output()
			if waited := time.Since(began); waited > WaitDelay+3*time.Second {
				t.Fatalf("Output waited %s for a command whose context ended after 100ms", waited)
			}
		})
	}
}
