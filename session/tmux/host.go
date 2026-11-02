package tmux

import (
	"os"
	"os/exec"
	"strings"
)

// HostManagedSession names the Adroit-managed tmux session this process is
// running inside, or "" when it is not inside one.
//
// Adroit holds a hidden tmux client on every session it manages, sized to the
// preview pane. Started from inside one of those sessions -- easy after
// tmux-continuum restores them and the terminal lands in one -- it attaches a
// second, smaller client to the very window it draws in. tmux then resizes
// that window between the two clients' sizes and the preview captures Adroit's
// own screen, so the UI flickers and jumps between sizes without end.
func HostManagedSession() string {
	pane := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || pane == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if !strings.HasPrefix(name, TmuxPrefix) {
		return ""
	}
	return name
}
