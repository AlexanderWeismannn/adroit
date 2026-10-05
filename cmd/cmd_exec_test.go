package cmd

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
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
