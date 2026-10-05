package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

// The pid file outlives the daemon -- a reboot, a crash -- and the OS reuses the
// pid. StopDaemon runs on every launch, so killing whatever holds that pid now
// once SIGKILLed the user's tmux server. A stale file must be removed and the
// process it now names left alone.
func TestStopDaemonLeavesAReusedPidAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A grandchild, not a child: a killed child of this test would linger as a
	// zombie and still answer kill(pid, 0), passing a test it should fail.
	out, err := exec.Command("sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatalf("start bystander: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("bystander pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	dir := filepath.Join(home, ".adroit")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644); err != nil {
		t.Fatal(err)
	}

	if err := StopDaemon(); err != nil {
		t.Fatalf("StopDaemon() = %v, want nil for a stale pid file", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("StopDaemon killed an unrelated process that had the daemon's old pid: %v", err)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("the stale pid file is still there (err=%v); every launch would try again", err)
	}
}
