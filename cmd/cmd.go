package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Executor interface {
	Run(cmd *exec.Cmd) error
	Output(cmd *exec.Cmd) ([]byte, error)
}

// Timeout is the ceiling on any one command run through Exec. Everything that
// goes through it is a tmux call that answers in milliseconds, several of them
// on every tick; one that has not answered in this long is a server that has
// stopped answering, and waiting on it would freeze every row's status with it.
const Timeout = 10 * time.Second

type Exec struct{}

func (e Exec) Run(cmd *exec.Cmd) error {
	_, err := run(cmd, false)
	return err
}

func (e Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	return run(cmd, true)
}

// run starts the command, bounds it by Timeout, and puts what it wrote to stderr
// into the error. Without that every failure read "exit status 1", which says
// neither which command it was nor why: the log could not tell a session that
// had gone from a server that had crashed.
func run(cmd *exec.Cmd, capture bool) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	if capture {
		if cmd.Stdout != nil {
			return nil, errors.New("exec: Stdout already set")
		}
		cmd.Stdout = &stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(Timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return stdout.Bytes(), &Error{Cmd: ToString(cmd), Stderr: strings.TrimSpace(stderr.String()), Err: err}
		}
		return stdout.Bytes(), nil
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return stdout.Bytes(), &Error{Cmd: ToString(cmd), Err: fmt.Errorf("timed out after %s", Timeout)}
	}
}

// Error is a failed command with what it said about it. It unwraps to the
// underlying error, so errors.As still finds an *exec.ExitError for a caller
// that needs the exit code.
type Error struct {
	Cmd    string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, e.Stderr)
	}
	return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// IsNoSuchSession reports whether a tmux command failed because the session it
// named does not exist -- which, for a kill, is the outcome that was wanted.
func IsNoSuchSession(err error) bool {
	var e *Error
	return errors.As(err, &e) && (strings.Contains(e.Stderr, "can't find session") ||
		strings.Contains(e.Stderr, "no server running") || strings.Contains(e.Stderr, "session not found"))
}

// NonInteractiveGit is the environment for a git command run on the user's
// behalf in the background. Nothing Adroit runs can be answered: its terminal is
// drawing the interface, so a credential prompt from an expired token or an ssh
// passphrase writes over the screen and swallows keystrokes until it times out --
// once a minute, from the upstream fetch. With these a fetch that needs a human
// fails at once instead, and says so in the log.
//
// GIT_OPTIONAL_LOCKS=0 as well: the status and diff reads on every tick would
// otherwise take index.lock to refresh the index, racing the agent's own git
// commands in the same worktree ("index.lock: File exists").
func NonInteractiveGit(cmd *exec.Cmd) *exec.Cmd {
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
	}
	cmd.Env = env
	return cmd
}

func MakeExecutor() Executor {
	return Exec{}
}

func ToString(cmd *exec.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	return strings.Join(cmd.Args, " ")
}
