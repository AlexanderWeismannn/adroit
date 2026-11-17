//go:build windows

package cmd

import "os/exec"

// Detach bounds c's Wait. Windows has no controlling terminal for a child to
// reach around its stdin, so there is nothing else to cut it off from.
func Detach(c *exec.Cmd) *exec.Cmd {
	if c.WaitDelay == 0 {
		c.WaitDelay = WaitDelay
	}
	return c
}
