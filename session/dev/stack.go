package dev

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
)

// SessionName is the tmux session the stack runs in, before the adroit_ prefix.
// One fixed name, not one per instance: the stack is a singleton, and a fixed
// name is what lets a restarted Adroit find a stack it started last time.
//
// A var rather than a const only so tests can point themselves at a name of
// their own. They drive real tmux sessions, and against the fixed name `go test`
// would tear down the stack the developer running it has open.
var SessionName = "dev"

const (
	// portReleaseTimeout bounds the wait for a torn-down stack to give its ports
	// up. Generous, because this is the ordinary path and the ports will free.
	portReleaseTimeout = 15 * time.Second
	// portGrace is the wait when nothing was torn down. A port busy here is held
	// by a stranger and is not about to be released, so waiting the full timeout
	// only delays the message that says so.
	portGrace = 2 * time.Second
)

// Status is a snapshot of the stack for rendering. It is a value, copied out
// under the lock, so the UI never reads fields the poller is writing.
type Status struct {
	// Running is whether the tmux session exists.
	Running bool
	// Title is the instance the stack is pointed at, "" when it is not running.
	Title string
	// Worktree is that instance's worktree.
	Worktree string
	// Lamps is one entry per configured check.
	Lamps []Lamp
	// Ready is whether every lamp is up.
	Ready bool
	// Phase is a one-line description of what the stack is doing.
	Phase string
	// Err is the last failure, if the stack could not be started.
	Err string
}

// Stack owns the singleton development stack.
type Stack struct {
	mu sync.Mutex
	// resolve answers which definition runs a given repository. Nil falls back
	// to fallback for everything, which is what New alone gives you.
	resolve func(repoPath string) *config.DevConfig
	// fallback is the definition used when resolve is nil.
	fallback *config.DevConfig
	// cfg is the definition the RUNNING stack was started from. It has to
	// outlive the selection: Stop waits on the ports of the stack it is tearing
	// down, and Poll probes the checks of the one that is up -- neither of which
	// is necessarily the repository the cursor is on.
	cfg *config.DevConfig
	// sel is the definition `d` would start for the selected repository. What
	// the interface gates and sizes itself from when nothing is running.
	sel      *config.DevConfig
	tmux     *tmux.TmuxSession
	title    string
	worktree string
	lamps    []Lamp
	phase    string
	err      string
	started  time.Time
	// opened latches the browser launch, so the URL opens once per start rather
	// than on every poll after the lamps go green.
	opened bool
	// starting suppresses polling while Start is tearing down and bringing up,
	// where a lamp reading is either stale or about to be.
	starting bool
	// polling guards against a second poll starting while the first is still in
	// flight. Poll runs detached from the caller's goroutine, and a probe can
	// take up to probeTimeout, so without this a slow check would accumulate one
	// overlapping poll per tick.
	polling bool
	// polledAt is when the last poll finished, which paces the steady state --
	// see steadyInterval.
	polledAt time.Time
}

// steadyInterval is how often a stack whose lamps are all up is re-probed.
//
// Readiness is urgent exactly once, while the stack comes up: that is what the
// lamps are watched for, and there it polls on every tick. Once everything is
// green the probes are a heartbeat, and running four of them twice a second
// forever -- two of which are HTTP requests against the user's own dev server,
// landing in its access log -- buys nothing but load. The cost of the slower
// cadence is that a server that dies is shown as down within this interval
// rather than within half a second.
const steadyInterval = 5 * time.Second

// New builds the stack manager with one definition used for every repository.
// cfg may be nil, which disables the feature until a resolver is installed.
func New(cfg *config.DevConfig) *Stack {
	return &Stack{fallback: cfg, sel: cfg}
}

// SetResolver installs the per-repository lookup. It replaces the definition
// passed to New for every repository, including the ones the resolver answers
// nil for -- a repository with no stack has no stack, rather than borrowing
// another project's.
func (s *Stack) SetResolver(fn func(repoPath string) *config.DevConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve = fn
	s.sel = nil
}

// Point tells the stack which repository the cursor is on, so that the key
// gating and the lamp header describe the stack `d` would start here rather than
// whichever one was configured first. Ignored while a stack is running: what is
// on screen then is that stack's output, and its own lamps are what belong above
// it.
func (s *Stack) Point(repoPath string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sel = s.lookupLocked(repoPath)
}

// lookupLocked resolves a repository's definition. Callers hold the mutex.
func (s *Stack) lookupLocked(repoPath string) *config.DevConfig {
	if s.resolve != nil {
		return s.resolve(repoPath)
	}
	return s.fallback
}

// activeLocked is the definition the interface should describe: the running
// stack's while one is up, otherwise the selected repository's. Callers hold the
// mutex.
func (s *Stack) activeLocked() *config.DevConfig {
	if s.tmux != nil {
		return s.cfg
	}
	return s.sel
}

// CheckCount is how many readiness lamps the stack has. Read from the config
// rather than from the current lamps so it is stable before the first poll --
// the pane sizes its header from this, and a header that changed height as the
// lamps arrived would shift the output under it.
func (s *Stack) CheckCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.activeLocked()
	if cfg == nil {
		return 0
	}
	return len(cfg.Checks)
}

// Configured reports whether there is a stack to show or start right now --
// the running one, or the selected repository's.
func (s *Stack) Configured() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.activeLocked()
	return cfg != nil && strings.TrimSpace(cfg.Command) != ""
}

// Adopt re-attaches to a stack left running by a previous Adroit process. The
// tmux session outlives the app, so without this a restart would show no stack
// while one is still holding the ports -- and then fail to start a new one.
func (s *Stack) Adopt(title, worktree, repoPath string) {
	if s == nil || title == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// The running stack's own definition, not the selected repository's: this is
	// the session that is already up, and it was started from repoPath's.
	cfg := s.lookupLocked(repoPath)
	if cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return
	}
	ts := tmux.NewTmuxSession(SessionName, cfg.Command)
	if !ts.DoesSessionExist() {
		return
	}
	s.cfg = cfg
	s.tmux = ts
	s.title = title
	s.worktree = worktree
	// Seeded now rather than left empty until the first poll: a caller sizing
	// itself from the lamps would otherwise lay out for none and then re-lay out
	// a moment later, moving everything it had already drawn.
	s.lamps = pendingLamps(s.cfg.Checks)
	s.phase = "adopted a running stack"
	// Not opened: a stack we did not start is one the user already has a window
	// on, and stealing focus to re-open it on the first poll would be rude.
	s.opened = true
	log.InfoLog.Printf("dev stack: adopted running session for %q", title)
}

// RunningFor returns the instance title the stack is pointed at, or "".
func (s *Stack) RunningFor() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// Snapshot copies the current state out for rendering.
func (s *Stack) Snapshot() Status {
	if s == nil {
		return Status{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st := Status{
		Title:    s.title,
		Worktree: s.worktree,
		Lamps:    append([]Lamp(nil), s.lamps...),
		Phase:    s.phase,
		Err:      s.err,
	}
	st.Running = s.tmux != nil && s.tmux.DoesSessionExist()
	st.Ready = st.Running && Up(st.Lamps)
	return st
}

// State collapses the lamps into the single verdict a list row has space for.
// Down wins over pending: a failed check is the one thing worth interrupting a
// glance for, and a stack with one red lamp is not "coming up".
func (st Status) State() LampState {
	if !st.Running {
		return LampDown
	}
	worst := LampUp
	for _, l := range st.Lamps {
		switch l.State {
		case LampDown:
			return LampDown
		case LampPending:
			worst = LampPending
		}
	}
	if len(st.Lamps) == 0 {
		return LampPending
	}
	return worst
}

// Capture returns the stack pane's current contents for display.
func (s *Stack) Capture() (string, error) {
	s.mu.Lock()
	ts := s.tmux
	s.mu.Unlock()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	return ts.CapturePaneContent()
}

// CaptureHistory returns the stack pane's full scrollback.
func (s *Stack) CaptureHistory() (string, error) {
	s.mu.Lock()
	ts := s.tmux
	s.mu.Unlock()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	return ts.CapturePaneContentWithOptions("-", "-")
}

// SetSize tells the detached pane how wide to wrap its output.
func (s *Stack) SetSize(width, height int) {
	s.mu.Lock()
	ts := s.tmux
	s.mu.Unlock()
	if ts == nil || width <= 0 || height <= 0 {
		return
	}
	if err := ts.SetDetachedSize(width, height); err != nil {
		log.InfoLog.Printf("dev stack: failed to set pane size: %v", err)
	}
}

// Attach opens the stack pane full-screen.
func (s *Stack) Attach() (chan struct{}, error) {
	s.mu.Lock()
	ts := s.tmux
	s.mu.Unlock()
	if ts == nil || !ts.DoesSessionExist() {
		return nil, fmt.Errorf("no dev stack is running")
	}
	return ts.Attach()
}

// Stop tears the stack down and waits for its ports to come free.
func (s *Stack) Stop() error {
	s.mu.Lock()
	ts, cfg := s.tmux, s.cfg
	s.tmux, s.title, s.worktree, s.lamps = nil, "", "", nil
	s.phase, s.err, s.opened = "", "", false
	s.mu.Unlock()

	if ts == nil {
		return nil
	}
	// Before the session goes: killing it first would orphan everything under it.
	killPaneTree(tmux.TmuxPrefix + SessionName)
	if err := ts.Close(); err != nil {
		// A session that is already gone is the outcome we wanted.
		log.InfoLog.Printf("dev stack: closing session: %v", err)
	}
	waitPortsFree(cfg, portReleaseTimeout)
	return nil
}

// Start points the stack at an instance, tearing down whatever it was on.
//
// Blocking, and slow by design -- it waits for ports to be released and for
// shared services to come up. Callers run it off the UI loop.
func (s *Stack) Start(title, worktree, repoPath string) error {
	// Resolved from the repository being started rather than from whatever the
	// cursor last pointed at: this runs off the UI loop, and the selection is
	// free to move between the keypress and here.
	s.mu.Lock()
	cfg := s.lookupLocked(repoPath)
	s.mu.Unlock()

	if cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return fmt.Errorf("no dev stack is configured for %s: add an entry under \"dev_by_repo\" in the Adroit config", repoDescription(repoPath))
	}
	if worktree == "" {
		return fmt.Errorf("session %s has no worktree to run a stack in", title)
	}

	// One start at a time. The key is easy to press twice while a slow stack is
	// coming up, and a second Start would tear down the first one's session
	// halfway through its own launch -- leaving a tmux session nothing owns and
	// two goroutines racing to write the same fields.
	s.mu.Lock()
	if s.starting {
		s.mu.Unlock()
		return fmt.Errorf("the dev stack is already starting; give it a moment")
	}
	s.starting = true
	s.phase = "stopping the previous stack"
	s.err = ""
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.starting = false
		s.mu.Unlock()
	}()

	// Tear the old one down first, even when it is already this instance: `d` on
	// the running session is a restart, which is the gesture you want after a
	// dependency change that nodemon does not pick up.
	if err := s.Stop(); err != nil {
		return err
	}

	// Only now, once the previous stack is down: Stop waits on the ports named by
	// the definition that stack was started from, and overwriting cfg any earlier
	// would have it wait on this repository's ports instead.
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	// A session left by a previous Adroit process holds the ports just as firmly.
	grace := portGrace
	if stale := tmux.NewTmuxSession(SessionName, cfg.Command); stale.DoesSessionExist() {
		killPaneTree(tmux.TmuxPrefix + SessionName)
		_ = stale.Close()
		grace = portReleaseTimeout
	}

	// Checked unconditionally, not only when something was torn down. The ports
	// can be held by things this stack never started -- a process outliving the
	// session that was killed a moment ago, a hand-run server in another terminal
	// -- and launching into one produces EADDRINUSE, which surfaces as a lamp
	// that never goes green and says nothing about why.
	waitPortsFree(cfg, grace)
	if held := portsStillHeld(cfg); len(held) > 0 {
		err := fmt.Errorf("cannot start the stack: %s", strings.Join(held, "; "))
		s.fail(err)
		return err
	}

	s.mu.Lock()
	s.title, s.worktree = title, worktree
	s.lamps = pendingLamps(cfg.Checks)
	s.phase = "starting shared services"
	s.mu.Unlock()

	if err := s.ensureServices(cfg, worktree); err != nil {
		s.fail(err)
		return err
	}

	s.mu.Lock()
	s.phase = "launching the stack"
	s.mu.Unlock()

	ts := tmux.NewTmuxSession(SessionName, buildCommand(cfg))
	if err := ts.Start(worktree); err != nil {
		err = fmt.Errorf("failed to start the dev stack: %w", err)
		s.fail(err)
		return err
	}

	s.mu.Lock()
	s.tmux = ts
	s.started = time.Now()
	s.opened = false
	s.phase = "waiting for the stack to come up"
	s.mu.Unlock()

	log.InfoLog.Printf("dev stack: started for %q in %s", title, worktree)
	return nil
}

// ensureServices runs the StartCommand of any check that is failing before the
// stack starts. These are shared services the stack needs but does not own -- a
// system Redis -- so they are started once, not supervised.
func (s *Stack) ensureServices(cfg *config.DevConfig, worktree string) error {
	for _, c := range cfg.Checks {
		if c.StartCommand == "" || probe(c, worktree).State == LampUp {
			continue
		}

		log.InfoLog.Printf("dev stack: %s is down, running its start command", c.Name)
		s.mu.Lock()
		s.phase = "starting " + c.Name
		s.mu.Unlock()

		out, err := exec.Command("sh", "-c", c.StartCommand).CombinedOutput()
		if err != nil {
			// Almost always a sudo prompt with nowhere to go. Hand the command
			// back rather than hanging on a password nobody can type.
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			return fmt.Errorf("could not start %s (%s) — run it yourself: %s",
				c.Name, firstLine(detail), c.StartCommand)
		}

		if !waitFor(func() bool { return probe(c, worktree).State == LampUp }, 10*time.Second) {
			return fmt.Errorf("%s did not come up after: %s", c.Name, c.StartCommand)
		}
	}
	return nil
}

// Poll refreshes the lamps and opens the configured URL the first time they are
// all up.
//
// Must not be called on a goroutine anything waits for. Every probe is a network
// operation bounded by probeTimeout, so a firewalled port or a hung server makes
// this take seconds; on the metadata tick that stalled the diff and status
// updates of every session behind it. It writes its results into the stack under
// the mutex and the interface reads them on its next render, so the caller has
// nothing to wait for -- see the go statement at its call site.
func (s *Stack) Poll() {
	if !s.Configured() {
		return
	}

	s.mu.Lock()
	if s.starting || s.tmux == nil || s.polling {
		s.mu.Unlock()
		return
	}
	// All lamps up is the steady state, and it is re-probed on a slower clock.
	// Anything else -- coming up, one check down, nothing polled yet -- keeps the
	// caller's cadence, which is where watching the lamps is the point.
	if Up(s.lamps) && time.Since(s.polledAt) < steadyInterval {
		s.mu.Unlock()
		return
	}
	s.polling = true
	worktree, checks := s.worktree, s.cfg.Checks
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.polling = false
		s.polledAt = time.Now()
		s.mu.Unlock()
	}()

	// Probed together rather than in sequence: they are independent, and in
	// sequence the worst case is the sum of every timeout instead of the longest
	// one -- four checks that all time out took six seconds to report.
	lamps := make([]Lamp, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c config.DevCheck) {
			defer wg.Done()
			lamps[i] = probe(c, worktree)
		}(i, c)
	}
	wg.Wait()
	ready := Up(lamps)

	// Past the budget, "still waiting" stops being true and starts being a claim
	// the pane cannot support. A lamp that has had three minutes is not pending.
	s.mu.Lock()
	expired := !ready && !s.started.IsZero() &&
		time.Since(s.started) > time.Duration(s.cfg.ReadyTimeoutSecs())*time.Second
	s.mu.Unlock()
	if expired {
		for i := range lamps {
			if lamps[i].State == LampPending {
				lamps[i].State = LampDown
				lamps[i].Detail = "timed out — " + lamps[i].Detail
			}
		}
	}

	s.mu.Lock()
	// Start may have swapped the stack out from under this poll; its lamps are
	// about the previous worktree and must not overwrite the new ones.
	if s.starting || s.worktree != worktree {
		s.mu.Unlock()
		return
	}
	s.lamps = lamps
	if ready {
		s.phase = "running"
	}
	shouldOpen := ready && !s.opened && s.cfg.OpenURL != ""
	if shouldOpen {
		s.opened = true
	}
	url, openCmd := s.cfg.OpenURL, s.cfg.OpenCommand
	s.mu.Unlock()

	if shouldOpen {
		if err := OpenURL(url, openCmd); err != nil {
			log.WarningLog.Printf("dev stack: could not open %s: %v", url, err)
		}
	}
}

func (s *Stack) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err.Error()
	s.phase = "failed"
}

// buildCommand wraps the configured command with its environment and keeps the
// pane alive afterwards.
//
// The environment is baked into the command string rather than set on the tmux
// invocation: a new session inherits the tmux SERVER's environment filtered by
// update-environment, not the caller's, so exported vars silently do not arrive.
// A shell assignment prefix has no such ambiguity.
//
// The tail matters as much: tmux destroys a pane when its command exits, so a
// stack that dies on startup would take its own error message with it and leave
// an empty pane that reads exactly like "never started".
//
// It parks on a sleep rather than handing the pane an interactive shell. A login
// shell sources the user's rc files, and a substantial one -- a fetch banner, a
// prompt, an unconditional `cd` -- scrolls the very error the pane was kept
// alive to show off the top of it, which is worse than closing.
func buildCommand(cfg *config.DevConfig) string {
	var b strings.Builder
	for _, k := range sortedKeys(cfg.Env) {
		// `export K=V; ...` rather than the shorter `K=V cmd` prefix: an
		// assignment prefix applies to ONE simple command, so the moment a
		// configured command is a sequence -- `export PATH=…; npm run dev` is the
		// ordinary shape where a version manager is involved -- every variable
		// would silently reach the first statement only, and nothing that matters.
		fmt.Fprintf(&b, "export %s=%s; ", k, shellQuote(cfg.Env[k]))
	}
	b.WriteString(cfg.Command)
	return fmt.Sprintf(
		"%s; printf '\\n[dev stack exited with status %%s — press d to restart it]\\n' \"$?\"; "+
			"while :; do sleep 3600; done", b.String())
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Insertion sort: these maps hold a handful of keys and this avoids the import.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func pendingLamps(checks []config.DevCheck) []Lamp {
	lamps := make([]Lamp, 0, len(checks))
	for _, c := range checks {
		lamps = append(lamps, Lamp{Name: c.Name, State: LampPending, Detail: "waiting"})
	}
	return lamps
}

// waitPortsFree blocks until nothing is listening on the ports the stack owns.
//
// Killing the tmux session returns as soon as the processes are signalled, but
// node holds its listening socket for a moment longer. Starting the next stack
// inside that window loses the port: the web server exits with EADDRINUSE, and
// the React dev server is worse -- it sees the port taken and PROMPTS to pick
// another one, leaving the pane blocked on an answer nobody is there to give.
func waitPortsFree(cfg *config.DevConfig, timeout time.Duration) {
	if cfg == nil {
		return
	}
	var ports []string
	for _, c := range cfg.Checks {
		// Only the ports this stack binds. A shared service is supposed to keep
		// listening across a switch, so waiting for Redis to go away would just
		// burn the whole timeout.
		if c.OwnCwd {
			if p := portOf(c.Target); p != "" {
				ports = append(ports, p)
			}
		}
	}
	if len(ports) == 0 {
		return
	}

	waitFor(func() bool {
		for _, p := range ports {
			conn, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 200*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return false
			}
		}
		return true
	}, timeout)
}

// portsStillHeld describes any port the stack needs that something else is on,
// naming the holder where it can be resolved. Reported before launching rather
// than diagnosed afterwards: the failure is otherwise a stack trace buried in a
// pane, or nothing at all if the process dies before anyone looks.
func portsStillHeld(cfg *config.DevConfig) []string {
	var held []string
	for _, c := range cfg.Checks {
		if !c.OwnCwd {
			continue
		}
		port := portOf(c.Target)
		if port == "" {
			continue
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()

		desc := fmt.Sprintf("port %s (%s) is already in use", port, c.Name)
		if owner, ownerErr := portOwnerCwd(c.Target); ownerErr == nil && owner != "" {
			desc += " by a process in " + shorten(owner)
		}
		held = append(held, desc)
	}
	return held
}

// waitFor polls cond every 250ms until it holds or timeout elapses.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// repoDescription names a repository in an error the user reads. The base name
// is what they call it; the empty case is a session with no repository to name,
// which is rare enough not to deserve a worse sentence.
func repoDescription(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return "this repository"
	}
	return filepath.Base(filepath.Clean(repoPath))
}
