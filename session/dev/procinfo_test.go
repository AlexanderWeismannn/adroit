package dev

import (
	"net"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// withoutProcFS runs a test as a system that has no /proc -- macOS -- forcing
// every process question down the lsof/ps path. Without this the fallbacks would
// only ever run on the platform where a break in them is discovered rather than
// prevented.
func withoutProcFS(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"lsof", "ps"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH, cannot stand in for macOS", tool)
		}
	}

	procFS, tools := hasProcFS, listenerTools
	hasProcFS, listenerTools = false, []string{"lsof"}
	resetListenerCache(t)
	t.Cleanup(func() { hasProcFS, listenerTools = procFS, tools })
}

// own_cwd is what stops another worktree's server turning this session's lamps
// green. On macOS the /proc readlink behind it does not exist, so without the
// lsof path the check silently reports "owner not resolved" forever.
func TestPortOwnerCwdWithoutProcFS(t *testing.T) {
	withoutProcFS(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	owner, err := portOwnerCwd(ln.Addr().String())
	require.NoError(t, err)

	want, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, want, owner, "the listening process is this test, so its cwd is ours")
}

// The teardown walks children by parent, because a detached child is still a
// child. Reading /proc/<pid>/stat is how that is done on Linux and there is no
// such file on macOS -- where an empty table collapses the walk to the pane
// leaders and leaves watchers holding ports.
func TestProcessParentsWithoutProcFS(t *testing.T) {
	withoutProcFS(t)

	parents := processParents()
	require.NotEmpty(t, parents, "ps should see the process table")

	self := os.Getpid()
	ppid, ok := parents[self]
	require.True(t, ok, "this process should be in the table")
	require.Equal(t, os.Getppid(), ppid)
}

// Liveness paces the escalation from TERM to KILL. os.Stat("/proc/<pid>") always
// fails on macOS, which reported every process dead the instant it was signalled
// and ended the teardown before it had done anything.
func TestLivePIDsWithoutProcFS(t *testing.T) {
	withoutProcFS(t)

	// A process that has certainly exited, and one that certainly has not.
	done := exec.Command("true")
	require.NoError(t, done.Run())
	dead := done.Process.Pid

	alive := livePIDs([]int{os.Getpid(), dead})
	require.Contains(t, alive, os.Getpid(), "this process is alive")
	require.NotContains(t, alive, dead, "a reaped process is not")
	require.Empty(t, livePIDs(nil))
}

// The two listings are parsed into the same table, so the rest of the package
// cannot tell which tool produced it.
func TestListenerParsersAgreeOnShape(t *testing.T) {
	ss := parseSSListeners(
		"LISTEN 0 511 127.0.0.1:3000 0.0.0.0:* users:((\"node\",pid=8123,fd=20))\n" +
			"LISTEN 0 511 [::1]:6379 [::]:* users:((\"redis\",pid=99,fd=6))\n" +
			// A port whose owner belongs to another user: held, but unattributable.
			"LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\n")
	require.Equal(t, []int{8123}, ss["3000"])
	require.Equal(t, []int{99}, ss["6379"])

	pids, held := ss["22"]
	require.True(t, held, "a port with no readable owner is still listed")
	require.Empty(t, pids, "and has no pid to attribute it to")

	lsof := parseLsofListeners("p8123\nn127.0.0.1:3000\nn[::1]:3000\np99\nn*:6379\n")
	require.Equal(t, []int{8123}, lsof["3000"], "one pid on two addresses is listed once")
	require.Equal(t, []int{99}, lsof["6379"])
}
