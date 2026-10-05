package config

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lock is what stops two Adroits driving one set of tmux sessions, and the
// way you end up with two is not carelessness -- closing a terminal window
// leaves the one inside it running.
func TestOnlyOneInstanceHoldsTheLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first, holder, err := AcquireInstanceLock()
	require.NoError(t, err)
	require.NotNil(t, first, "nothing else holds it")
	assert.Zero(t, holder)

	second, holder, err := AcquireInstanceLock()
	require.NoError(t, err, "a taken lock is an answer, not a failure")
	assert.Nil(t, second)
	assert.Equal(t, os.Getpid(), holder, "the holder is named so the user can go and find it")

	require.NoError(t, first.Release())

	third, _, err := AcquireInstanceLock()
	require.NoError(t, err)
	require.NotNil(t, third, "releasing lets the next one in")
	require.NoError(t, third.Release())
}

// Release must be safe to defer before knowing whether the lock was taken.
func TestReleasingANilLockDoesNothing(t *testing.T) {
	var lock *Lock
	assert.NoError(t, lock.Release())
}

// The pid is written so a second instance can say where the first one is. A
// lock file left holding a longer pid must not leave its tail behind.
func TestTheLockFileHoldsOnlyTheCurrentPID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, ".adroit", LockFileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("2147483647"), 0o644))

	lock, _, err := AcquireInstanceLock()
	require.NoError(t, err)
	require.NotNil(t, lock)
	t.Cleanup(func() { _ = lock.Release() })

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(contents))
}

// A lock held by a process that was killed with its terminal has to clear
// itself: the whole point is to guard against an Adroit that did not shut down
// cleanly, and one that needed a clean shutdown to release would have to be
// cleared by hand exactly then.
func TestTheLockDiesWithItsHolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".adroit"), 0o755))

	// One process and no children, so killing it really does drop every
	// reference to the locked file: `read` is a shell builtin, and stdin is a
	// pipe nothing ever writes to.
	holder := exec.Command("sh", "-c", `exec 9>>"$1"; flock -x 9 || exit 1; echo ready; read line`, "sh",
		filepath.Join(home, ".adroit", LockFileName))
	stdin, _, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })
	holder.Stdin = stdin

	out, err := holder.StdoutPipe()
	require.NoError(t, err)
	if err := holder.Start(); err != nil {
		t.Skipf("no shell with flock available: %v", err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})
	if _, err := io.ReadFull(out, make([]byte, len("ready\n"))); err != nil {
		t.Skipf("holder did not take the lock: %v", err)
	}

	taken, _, err := AcquireInstanceLock()
	require.NoError(t, err)
	require.Nil(t, taken, "the child holds it")

	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()

	lock, _, err := AcquireInstanceLock()
	require.NoError(t, err)
	require.NotNil(t, lock, "a killed holder leaves no lock behind")
	require.NoError(t, lock.Release())
}
