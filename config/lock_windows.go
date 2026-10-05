//go:build windows

package config

import "os"

// Windows has no flock. Adroit has never run there -- it drives tmux -- so
// rather than reach for LockFileEx, the lock is simply not taken, and the
// build stays honest about which platforms it covers.
func tryLockFile(file *os.File) (bool, error) { return true, nil }

func unlockFile(file *os.File) error { return nil }
