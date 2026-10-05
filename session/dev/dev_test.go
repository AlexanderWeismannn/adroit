package dev

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

func TestIsUnderRejectsSiblingWithSharedPrefix(t *testing.T) {
	// The failure this exists for: /repo-backup starts with /repo, so a plain
	// string prefix would attribute another checkout's server to this worktree
	// and light every lamp green on the wrong branch.
	cases := []struct {
		path, worktree string
		want           bool
	}{
		{"/home/a/wt", "/home/a/wt", true},
		{"/home/a/wt/client", "/home/a/wt", true},
		{"/home/a/wt-backup", "/home/a/wt", false},
		{"/home/a/wt2", "/home/a/wt", false},
		{"/home/a", "/home/a/wt", false},
		{"/home/a/wt/./client", "/home/a/wt", true},
	}
	// The kernel reports a process's cwd by its real path; the worktree may be
	// reached through a symlink, as every macOS temp directory is.
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "client"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "wt")
	if err := os.Symlink(real, link); err == nil {
		cases = append(cases, struct {
			path, worktree string
			want           bool
		}{filepath.Join(real, "client"), link, true})
	}
	for _, c := range cases {
		if got := isUnder(c.path, c.worktree); got != c.want {
			t.Errorf("isUnder(%q, %q) = %v, want %v", c.path, c.worktree, got, c.want)
		}
	}
}

func TestPortOfReadsEveryTargetForm(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:3000":              "3000",
		"http://127.0.0.1:3001/livez": "3001",
		"https://localhost:5001":      "5001",
		"[::1]:6379":                  "6379",
		"0.0.0.0:*":                   "",
		"nonsense":                    "",
	}
	for in, want := range cases {
		if got := portOf(in); got != want {
			t.Errorf("portOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeTCPReportsPendingWhenNothingListens(t *testing.T) {
	// Bind and release, so the port is one nothing is on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	lamp := probe(config.DevCheck{Name: "web", Type: "tcp", Target: addr}, "")
	if lamp.State != LampPending {
		t.Fatalf("state = %v, want LampPending (detail %q)", lamp.State, lamp.Detail)
	}
}

func TestProbeHTTPAcceptsAnyResponseWhenNoStatusRequired(t *testing.T) {
	// A dev server behind auth answers 401 to an unauthenticated probe. That
	// proves it is listening, which is the whole question a lamp asks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if lamp := probe(config.DevCheck{Name: "web", Type: "http", Target: srv.URL}, ""); lamp.State != LampUp {
		t.Fatalf("state = %v, want LampUp (detail %q)", lamp.State, lamp.Detail)
	}

	strict := config.DevCheck{Name: "worker", Type: "http", Target: srv.URL, ExpectStatus: 200}
	if lamp := probe(strict, ""); lamp.State != LampPending {
		t.Fatalf("strict state = %v, want LampPending (detail %q)", lamp.State, lamp.Detail)
	}
}

// ssAvailable skips a test on a box where port ownership cannot be resolved at
// all, so the suite reports "not exercised" rather than a false pass.
func ssAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip("ss is not on PATH; port ownership cannot be verified here")
	}
}

func TestPortOwnerCwdResolvesTheListeningProcess(t *testing.T) {
	ssAvailable(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	owner, err := portOwnerCwd(ln.Addr().String())
	if err != nil {
		t.Fatalf("portOwnerCwd: %v", err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if owner != want {
		t.Fatalf("portOwnerCwd = %q, want this process's cwd %q", owner, want)
	}
}

func TestProbeOwnCwdRejectsAPortHeldByAnotherWorktree(t *testing.T) {
	ssAvailable(t)

	// A server that answers perfectly well -- from somewhere else. This is the
	// failure the whole check exists for: after switching sessions the previous
	// worktree's server still holds the port and responds identically, so every
	// lamp goes green while the code being served is the wrong branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	check := config.DevCheck{Name: "web", Type: "http", Target: srv.URL, OwnCwd: true}
	lamp := probe(check, t.TempDir())

	if lamp.State != LampDown {
		t.Fatalf("state = %v, want LampDown (detail %q)", lamp.State, lamp.Detail)
	}
	if !contains(lamp.Detail, "another worktree") {
		t.Fatalf("detail = %q, want it to name the cause", lamp.Detail)
	}
}

func TestProbeOwnCwdAcceptsAPortHeldByThisWorktree(t *testing.T) {
	ssAvailable(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	lamp := probe(config.DevCheck{Name: "web", Type: "http", Target: srv.URL, OwnCwd: true}, cwd)
	if lamp.State != LampUp {
		t.Fatalf("state = %v, want LampUp (detail %q)", lamp.State, lamp.Detail)
	}
	// "up (owner unknown)" would mean the check degraded instead of verifying,
	// which passes for the wrong reason.
	if lamp.Detail != "up" {
		t.Fatalf("detail = %q, want ownership to have been verified", lamp.Detail)
	}
}

func TestBuildCommandBakesEnvAndKeepsThePaneAlive(t *testing.T) {
	cfg := &config.DevConfig{
		Command: "npm run dev",
		Env:     map[string]string{"PORT": "3000", "BROWSER": "none"},
	}
	got := buildCommand(cfg)

	// Assignments must precede the command: tmux gives a new session the SERVER's
	// environment, not ours, so anything exported here would silently not arrive.
	want := "export BROWSER='none'; export PORT='3000'; npm run dev; "
	if len(got) < len(want) || got[:len(want)] != want {
		t.Fatalf("command = %q, want it to start %q", got, want)
	}
	// And the pane has to outlive the command, or a stack that dies on startup
	// takes its own error message with it.
	if !contains(got, "while :; do sleep 3600; done") {
		t.Fatalf("command = %q, want a keep-alive to hold the pane open", got)
	}
	// Specifically NOT an interactive shell: sourcing the user's rc files can
	// scroll the failure the pane was kept open to show off the top of it.
	if contains(got, "SHELL") {
		t.Fatalf("command = %q, must not hand the pane a login shell", got)
	}
}

func TestBuildCommandQuotesAwkwardValues(t *testing.T) {
	cfg := &config.DevConfig{
		Command: "run",
		Env:     map[string]string{"MSG": "it's a value; rm -rf /"},
	}
	got := buildCommand(cfg)
	if !contains(got, `export MSG='it'\''s a value; rm -rf /'; run`) {
		t.Fatalf("command = %q, want the value quoted whole", got)
	}
}

// The shape that broke the prefix form: a command that is a sequence, which is
// what any version-manager or PATH setup produces.
func TestBuildCommandEnvSurvivesASequencedCommand(t *testing.T) {
	cfg := &config.DevConfig{
		Command: `export PATH="$HOME/n/bin:$PATH"; npm run dev`,
		Env:     map[string]string{"PORT": "3000"},
	}
	got := buildCommand(cfg)
	// The assignment has to be its own statement, so it is still in scope by the
	// time the second half of the command runs.
	if !contains(got, "export PORT='3000'; export PATH=") {
		t.Fatalf("command = %q, want PORT exported before the sequence", got)
	}
}

func TestStatusStateReportsTheWorstLamp(t *testing.T) {
	cases := []struct {
		name string
		st   Status
		want LampState
	}{
		{"not running", Status{Running: false}, LampDown},
		{"all up", Status{Running: true, Lamps: []Lamp{{State: LampUp}, {State: LampUp}}}, LampUp},
		{"one pending", Status{Running: true, Lamps: []Lamp{{State: LampUp}, {State: LampPending}}}, LampPending},
		// Down beats pending: one red lamp means the stack is not coming up.
		{"one down", Status{Running: true, Lamps: []Lamp{{State: LampPending}, {State: LampDown}}}, LampDown},
		{"no checks", Status{Running: true}, LampPending},
	}
	for _, c := range cases {
		if got := c.st.State(); got != c.want {
			t.Errorf("%s: State() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnconfiguredStackRefusesToStart(t *testing.T) {
	isolateSession(t)
	s := New(nil)
	if s.Configured() {
		t.Fatal("a nil config must not read as configured")
	}
	if err := s.Start("x", t.TempDir(), ""); err == nil {
		t.Fatal("Start() on an unconfigured stack must fail")
	}
	// And every accessor has to stay safe: the app holds one of these always.
	if s.RunningFor() != "" || s.Snapshot().Running {
		t.Fatal("an unconfigured stack must report nothing running")
	}
	s.Poll()
}

func TestShortenKeepsTheTailOfAPath(t *testing.T) {
	got := shorten(filepath.Join("/home", "a", "worktrees", "TASK-1"))
	if got != ".../worktrees/TASK-1" {
		t.Fatalf("shorten() = %q", got)
	}
}

func TestOpenURLWithNoURLIsANoOp(t *testing.T) {
	if err := OpenURL("", nil); err != nil {
		t.Fatalf("OpenURL(\"\") = %v, want nil", err)
	}
}

func TestOpenURLUsesTheConfiguredCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	err := OpenURL("http://example.test", []string{"sh", "-c", fmt.Sprintf("printf %%s \"$1\" > %s", marker), "sh"})
	if err != nil {
		t.Fatalf("OpenURL = %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	// The URL is appended as the final argument, which is what makes an opener
	// configurable without a placeholder syntax.
	if string(got) != "http://example.test" {
		t.Fatalf("opener received %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// The key is easy to press twice while a slow stack comes up. The second press
// must not tear the first launch down halfway through it.
func TestStackRefusesAConcurrentStart(t *testing.T) {
	isolateSession(t)
	s := New(&config.DevConfig{Command: "true"})

	// Set directly rather than raced into: what is under test is the guard, and
	// timing a real start to land inside its own window tests the sleep instead.
	s.mu.Lock()
	s.starting = true
	s.mu.Unlock()

	err := s.Start("beta", t.TempDir(), "")
	if err == nil {
		t.Fatal("a concurrent Start must be refused")
	}
	if !contains(err.Error(), "already starting") {
		t.Fatalf("error = %v, want it to say the stack is already starting", err)
	}
}

// isolateSession points the stack at a tmux session name of this test's own, so
// a suite run never tears down the stack the developer running it has open.
func isolateSession(t *testing.T) {
	t.Helper()
	previous := SessionName
	SessionName = fmt.Sprintf("test-%s-%d", strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' {
			return '-'
		}
		return r
	}, t.Name()), os.Getpid())
	t.Cleanup(func() { SessionName = previous })
}

// Both own_cwd checks in a poll ask the same system-wide question, and a
// listener's pid does not change while it holds the port. Without the cache each
// check spawned its own scan -- two per poll, twice a second -- to parse the
// same table twice.
func TestListenersCachesTheListing(t *testing.T) {
	resetListenerCache(t)

	_, _, err := listenerPIDs("1", true)
	if err != nil {
		t.Skipf("no way to list listeners here: %v", err)
	}
	at := lsnAt

	_, _, err = listenerPIDs("1", false)
	require.NoError(t, err)
	require.Equal(t, at, lsnAt, "a second read inside the TTL must not re-run the scan")

	_, _, err = listenerPIDs("1", true)
	require.NoError(t, err)
	require.True(t, lsnAt.After(at), "a forced read refreshes")
}

// resetListenerCache empties the shared listing so a test starts from a known
// state and leaves nothing behind for the next one.
func resetListenerCache(t *testing.T) {
	t.Helper()
	clear := func() {
		lsnMu.Lock()
		lsnTable, lsnErr, lsnAt = nil, nil, time.Time{}
		lsnMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// A port that answers a dial but is missing from the listing means the listing
// is stale -- a server that has only just bound it -- and reporting "owner not
// resolved" for two seconds would flag a healthy stack as pending.
func TestPortOwnerCwdRefreshesAStaleListing(t *testing.T) {
	resetListenerCache(t)
	if _, _, err := listenerPIDs("1", true); err != nil {
		t.Skipf("no way to list listeners here: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	// Prime the cache with a listing taken before the port existed.
	lsnMu.Lock()
	lsnTable, lsnErr, lsnAt = map[string][]int{}, nil, time.Now()
	lsnMu.Unlock()

	owner, err := portOwnerCwd(ln.Addr().String())
	require.NoError(t, err)
	want, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, want, owner)
}

// Readiness is urgent while the stack comes up and a heartbeat afterwards. The
// steady cadence is what keeps four probes -- two of them HTTP requests against
// the user's own dev server -- off a twice-a-second clock forever.
func TestSteadyIntervalIsSlowerThanTheTick(t *testing.T) {
	require.Greater(t, steadyInterval, 500*time.Millisecond)
}

// The stack is a singleton, but which definition it runs is a question about the
// repository under the cursor. Gating and header sizing therefore have to follow
// the selection, and a start has to resolve from the repository it was asked
// for -- not from whichever one the cursor drifted to while it was starting.
func TestStackResolvesTheDefinitionPerRepository(t *testing.T) {
	isolateSession(t)

	web := &config.DevConfig{Command: "npm run dev", Checks: []config.DevCheck{
		{Name: "web", Type: "tcp", Target: "127.0.0.1:1"},
		{Name: "api", Type: "tcp", Target: "127.0.0.1:2"},
	}}
	cli := &config.DevConfig{Command: "cargo watch -x run"}

	s := New(nil)
	s.SetResolver(func(repoPath string) *config.DevConfig {
		switch repoPath {
		case "/srv/web":
			return web
		case "/srv/cli":
			return cli
		}
		return nil
	})

	s.Point("/srv/web")
	require.True(t, s.Configured(), "the selected repository has a stack")
	require.Equal(t, 2, s.CheckCount(), "the header sizes itself from the selected repository's checks")

	s.Point("/srv/cli")
	require.True(t, s.Configured())
	require.Equal(t, 0, s.CheckCount(), "a stack with no checks has no lamps")

	// The whole point of the change: a repository with no entry does not borrow
	// another project's stack.
	s.Point("/srv/unconfigured")
	require.False(t, s.Configured(), "a repository with no definition has no stack")
	require.Equal(t, 0, s.CheckCount())

	// Named in the error, so it is clear which repository needs the entry.
	err := s.Start("session", t.TempDir(), "/srv/unconfigured")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unconfigured")
	require.Contains(t, err.Error(), "dev_by_repo")
}

// Start reads the repository it was handed rather than whatever Point last saw:
// it runs off the UI loop, and the cursor is free to move in between.
func TestStackStartIgnoresTheSelectionItWasNotGiven(t *testing.T) {
	isolateSession(t)

	s := New(nil)
	s.SetResolver(func(repoPath string) *config.DevConfig {
		if repoPath == "/srv/real" {
			return &config.DevConfig{Command: "true"}
		}
		return nil
	})

	// The cursor has moved to a repository with no stack, but the start was asked
	// for the one that has one.
	s.Point("/srv/elsewhere")
	require.NoError(t, s.Start("session", t.TempDir(), "/srv/real"))
	t.Cleanup(func() { _ = s.Stop() })

	// And while it runs, the lamps and gating describe the stack that is up --
	// not the repository the cursor happens to be on.
	require.True(t, s.Configured(), "a running stack is configured whatever the cursor is on")
	require.Equal(t, "session", s.RunningFor())
}
