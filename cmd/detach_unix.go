//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// Detach runs c in a session of its own, with no controlling terminal, and
// bounds its Wait.
//
// Adroit's terminal is drawing the interface. A child that can reach it -- sudo
// asking for a password, ssh for a passphrase, a credential helper -- opens
// /dev/tty directly, whatever its stdin is, and draws its prompt over the screen
// while the keystrokes meant for Adroit are split between the two. In a session
// of its own there is no /dev/tty to open, so such a child fails at once and
// says why, which is what a background command should do.
//
// A command built with exec.CommandContext is cancelled by killing its whole
// process group rather than only the shell at its head, so a timeout takes the
// sudo or ssh beneath it down too.
func Detach(c *exec.Cmd) *exec.Cmd {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setsid = true
	if c.Cancel != nil {
		c.Cancel = func() error {
			// The new session's id is the child's pid, and so is its group's.
			return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	if c.WaitDelay == 0 {
		c.WaitDelay = WaitDelay
	}
	return c
}
