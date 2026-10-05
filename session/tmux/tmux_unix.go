//go:build !windows

package tmux

import (
	"github.com/AlexanderWeismannn/adroit/log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

// monitorWindowSize monitors and handles window resize events while attached.
func (t *TmuxSession) monitorWindowSize() {
	winchChan := make(chan os.Signal, 1)
	signal.Notify(winchChan, syscall.SIGWINCH)
	// Send initial SIGWINCH to trigger the first resize
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGWINCH)

	everyN := log.NewEvery(60 * time.Second)

	doUpdate := func() {
		// Use the current terminal height and width.
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err != nil {
			if everyN.ShouldLog() {
				log.ErrorLog.Printf("failed to update window size: %v", err)
			}
		} else {
			if err := t.updateWindowSize(cols, rows); err != nil {
				if everyN.ShouldLog() {
					log.ErrorLog.Printf("failed to update window size: %v", err)
				}
			}
		}
	}
	// Do one at the end of the function to set the initial size.
	defer doUpdate()

	// Taken now, for the reason given in Attach: a detach clears these and opens
	// a fresh set, and a goroutine still winding down would read nil.
	ctx, wg := t.ctx, t.wg

	// Debounce resize events
	wg.Add(2)
	debouncedWinch := make(chan os.Signal, 1)
	go func() {
		defer wg.Done()
		var resizeTimer *time.Timer
		for {
			select {
			case <-ctx.Done():
				return
			case <-winchChan:
				if resizeTimer != nil {
					resizeTimer.Stop()
				}
				resizeTimer = time.AfterFunc(50*time.Millisecond, func() {
					select {
					case debouncedWinch <- syscall.SIGWINCH:
					case <-ctx.Done():
					}
				})
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer signal.Stop(winchChan)
		// Handle resize events
		for {
			select {
			case <-ctx.Done():
				return
			case <-debouncedWinch:
				doUpdate()
			}
		}
	}()
}
