package dev_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/session/dev"
)

// Drives a real stack end to end against a trivial command, in a temp dir, on
// ports nothing else uses: start, come up, verify ownership, switch, tear down.
func TestStackStartsSwitchesAndStops(t *testing.T) {
	isolateSession(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	wtA, wtB := filepath.Join(home, "a"), filepath.Join(home, "b")
	for _, d := range []string{wtA, wtB} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.DevConfig{
		// Reads the port from the ENVIRONMENT at runtime, which is the thing under
		// test: the env has to reach the process, not merely the command line.
		// http.server.HTTPServer, not a bare socketserver.TCPServer: it sets
		// SO_REUSEADDR, as node and webpack-dev-server both do. Without it the
		// TIME_WAIT left by this test's own probes blocks the rebind, and the
		// test measures a socket option instead of the teardown sequence.
		Command: `python3 -c 'import os,http.server;` +
			`http.server.HTTPServer(("127.0.0.1", int(os.environ["SERVE_PORT"])),` +
			` http.server.SimpleHTTPRequestHandler).serve_forever()'`,
		Env:                 map[string]string{"SERVE_PORT": "39117"},
		Checks:              []config.DevCheck{{Name: "web", Type: "http", Target: "http://127.0.0.1:39117/", OwnCwd: true}},
		ReadyTimeoutSeconds: 30,
	}

	s := dev.New(cfg)
	t.Cleanup(func() { _ = s.Stop() })

	if err := s.Start("alpha", wtA, ""); err != nil {
		t.Fatalf("Start(alpha): %v", err)
	}
	waitReady(t, s, "alpha")

	if got := s.RunningFor(); got != "alpha" {
		t.Fatalf("RunningFor() = %q, want alpha", got)
	}

	// The switch: same port, different worktree. If the teardown did not wait for
	// the port, the new listener never binds and this stays red forever.
	if err := s.Start("beta", wtB, ""); err != nil {
		t.Fatalf("Start(beta): %v", err)
	}
	waitReady(t, s, "beta")

	// And the lamp must be attributing the port to the NEW worktree.
	st := s.Snapshot()
	if st.Lamps[0].Detail != "up" {
		t.Fatalf("after the switch the lamp reads %q; ownership was not re-verified", st.Lamps[0].Detail)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := s.Snapshot(); st.Running || st.Title != "" {
		b, _ := json.Marshal(st)
		t.Fatalf("after Stop the stack still reports %s", b)
	}
}

func waitReady(t *testing.T, s *dev.Stack, title string) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		s.Poll()
		if st := s.Snapshot(); st.Ready && st.Title == title {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, _ := json.Marshal(s.Snapshot())
	pane, err := s.CaptureHistory()
	if err != nil {
		pane = "(capture failed: " + err.Error() + ")"
	}
	t.Fatalf("stack for %s never came up: %s\n--- pane ---\n%s", title, b, pane)
}

// A port held by something the stack did not start must be reported before the
// launch, not left as a lamp that never goes green.
func TestStackRefusesToStartOntoAHeldPort(t *testing.T) {
	isolateSession(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	cfg := &config.DevConfig{
		Command:             "true",
		Checks:              []config.DevCheck{{Name: "web", Type: "tcp", Target: ln.Addr().String(), OwnCwd: true}},
		ReadyTimeoutSeconds: 5,
	}
	s := dev.New(cfg)
	t.Cleanup(func() { _ = s.Stop() })

	err = s.Start("alpha", t.TempDir(), "")
	if err == nil {
		t.Fatal("Start() onto a held port must fail")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("error = %v, want it to name the busy port", err)
	}
	// And it must say WHERE, so the answer is not "go find it yourself".
	if !strings.Contains(err.Error(), "by a process in") {
		t.Fatalf("error = %v, want it to name the holder", err)
	}
}

// The orphan shape, reproduced: a supervisor whose child runs in its OWN process
// group. That is what every process supervisor does so it can signal its
// commands independently, and it puts the child outside the group tmux signals
// when the session is killed -- so it survives, reparented to init, still
// holding whatever it had open. Stop must reach it anyway.
func TestStopKillsAChildInItsOwnProcessGroup(t *testing.T) {
	isolateSession(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(home, "grandchild.pid")

	cfg := &config.DevConfig{
		// setsid is the escape: the child leads a new session and group. Done in
		// python rather than with setsid(1), which macOS does not ship; it forks
		// first when it already leads a group, as setsid(1) does, and records its
		// own pid since that is the process that must die.
		Command: `python3 -c 'import os,sys,time` + "\n" +
			`if os.getpgrp() == os.getpid() and os.fork(): os._exit(0)` + "\n" +
			`os.setsid(); open(sys.argv[1], "w").write(str(os.getpid())); time.sleep(600)' ` +
			marker + ` & wait`,
		ReadyTimeoutSeconds: 5,
	}
	s := dev.New(cfg)
	t.Cleanup(func() { _ = s.Stop() })

	if err := s.Start("alpha", home, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var pid int
	if !waitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(marker)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pid > 1 && processAlive(pid)
	}) {
		t.Fatal("the escaped child never started")
	}
	t.Cleanup(func() { _ = exec.Command("sh", "-c", "kill -KILL "+strconv.Itoa(pid)+" 2>/dev/null").Run() })

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !waitUntil(8*time.Second, func() bool { return !processAlive(pid) }) {
		t.Fatalf("process %d survived Stop: it was orphaned, not killed", pid)
	}
}

// processAlive asks the kernel rather than /proc, which macOS does not have.
// EPERM still means the process exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

// isolateSession points the stack at a tmux session name of this test's own, so
// a suite run never tears down the stack the developer running it has open.
func isolateSession(t *testing.T) {
	t.Helper()
	previous := dev.SessionName
	dev.SessionName = fmt.Sprintf("test-%s-%d", strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' {
			return '-'
		}
		return r
	}, t.Name()), os.Getpid())
	t.Cleanup(func() { dev.SessionName = previous })
}
