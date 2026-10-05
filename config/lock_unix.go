//go:build !windows

package config

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock without blocking.
//
// flock rather than a pid file of our own: the kernel drops it when the process
// dies by any route, including the SIGKILL that comes with a closed terminal, so
// there is no stale lock to clean up and no window in which a recycled pid reads
// as a live holder.
func tryLockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
