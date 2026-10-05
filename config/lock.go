package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// LockFileName is the single-instance lock, held for as long as an interactive
// Adroit is running.
//
// Two Adroits on one machine are not merely redundant, they fight: each opens
// its own `tmux attach-session` client against every session, so the panes get
// resized to whichever one asked last, and each takes the terminal away from the
// other on attach. There was no guard at all, and the way you ended up with two
// was not carelessness -- closing a terminal window leaves the Adroit inside it
// running, so the next window's Adroit is the second one.
const LockFileName = "adroit.lock"

// Lock is a held single-instance lock. Releasing it is optional: the operating
// system drops the underlying file lock when the process exits, however it
// exits, which is the property that matters here -- the instance this is
// protecting against is one that was killed with its terminal window, and a
// lock that needed a clean shutdown to clear would have to be cleaned up by
// hand exactly then.
type Lock struct {
	file *os.File
}

// AcquireInstanceLock takes the single-instance lock.
//
// A nil Lock with a nil error means another Adroit holds it; holder is its pid,
// or 0 if the file did not say.
func AcquireInstanceLock() (lock *Lock, holder int, err error) {
	dir, err := GetConfigDir()
	if err != nil {
		return nil, 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("could not create %s: %w", dir, err)
	}

	path := filepath.Join(dir, LockFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, 0, fmt.Errorf("could not open the lock file %s: %w", path, err)
	}

	locked, err := tryLockFile(file)
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("could not lock %s: %w", path, err)
	}
	if !locked {
		// Read the pid before closing: it is the only thing that can tell the user
		// which process to go and find. A file whose holder has not written its pid
		// yet reads as 0, which is reported as "unknown" rather than guessed at.
		holder = readHolderPID(file)
		_ = file.Close()
		return nil, holder, nil
	}

	// Record who holds it, truncating first so a shorter pid cannot leave the
	// tail of a longer one behind.
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
		_ = file.Sync()
	}

	return &Lock{file: file}, 0, nil
}

// Release drops the lock. Safe on a nil Lock, so a caller can defer it without
// first checking whether it got one.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}

func readHolderPID(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// DescribeLockHolder says where to find the Adroit that holds the lock, in as
// much detail as the machine will give up.
//
// "Already running" on its own is a dead end: the process that holds the lock is
// in some other terminal, and if that terminal is gone the user has no way to
// tell which one. The tty narrows it to a window; the tmux session narrows it to
// a keystroke.
func DescribeLockHolder(pid int) string {
	if pid <= 0 {
		return "another Adroit process"
	}
	desc := fmt.Sprintf("pid %d", pid)

	tty := strings.TrimSpace(runOut("ps", "-o", "tty=", "-p", strconv.Itoa(pid)))
	if tty == "" || tty == "?" {
		return desc
	}
	desc += ", on /dev/" + tty

	// tmux reports pane ttys as absolute paths.
	for _, line := range strings.Split(runOut("tmux", "list-panes", "-a", "-F", "#{pane_tty} #{session_name}"), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "/dev/"+tty {
			desc += fmt.Sprintf(", in tmux session %q", fields[1])
			break
		}
	}
	return desc
}

func runOut(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
