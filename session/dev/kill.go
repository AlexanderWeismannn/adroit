package dev

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
)

// killPaneTree terminates every process descended from a tmux session's panes.
//
// Neither of the simpler things is enough. `tmux kill-session` signals the pane's
// process group, and a process supervisor -- the shape of every dev stack -- puts
// each command it runs in a NEW group precisely so it can signal them
// independently. Killed abruptly it never gets to, and those groups are outside
// the one tmux signalled. So they survive, reparented to init: a file watcher
// with no window showing it, still holding a port, still ready to restart the
// server it was watching for a worktree nobody is looking at any more.
//
// Walking children by parent covers that, because a detached child is still a
// CHILD -- it only stops being one once its parent dies. Which is why the tree is
// collected before anything is signalled, and signalled from the leaves up.
func killPaneTree(sessionName string) {
	roots := panePIDs(sessionName)
	if len(roots) == 0 {
		return
	}

	tree := descendants(roots)
	if len(tree) == 0 {
		return
	}

	signalAll(tree, "TERM")
	// A supervisor needs a moment to pass the signal on and reap. Past that, what
	// is left is not shutting down, and holding the ports open helps no one.
	if waitFor(func() bool { return !anyAlive(tree) }, 4*time.Second) {
		return
	}
	log.WarningLog.Printf("dev stack: %d process(es) survived SIGTERM, killing", countAlive(tree))
	signalAll(tree, "KILL")
	waitFor(func() bool { return !anyAlive(tree) }, 2*time.Second)
}

// descendants returns roots and everything below them, deepest first, so a
// supervisor is signalled after the children it would otherwise restart.
func descendants(roots []int) []int {
	children := childMap()

	var out []int
	var walk func(pid int, depth int)
	seen := map[int]bool{}
	walk = func(pid int, depth int) {
		// Cycles are impossible in a process tree, but a stale scan could still
		// produce one; bound it rather than hang the interface.
		if seen[pid] || depth > 32 {
			return
		}
		seen[pid] = true
		for _, child := range children[pid] {
			walk(child, depth+1)
		}
		out = append(out, pid)
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return out
}

// childMap inverts the system's parent table into parent -> children. Read once
// per teardown rather than per pid: how it is read at all is procinfo.go's
// problem, and without it the tree collapses to the pane leaders, which is the
// pre-tree behaviour and leaves detached children holding ports.
func childMap() map[int][]int {
	parents := processParents()
	if len(parents) == 0 {
		return nil
	}
	children := make(map[int][]int, len(parents))
	for pid, ppid := range parents {
		if ppid > 0 {
			children[ppid] = append(children[ppid], pid)
		}
	}
	return children
}

// panePIDs lists the leader process of every pane in the session.
func panePIDs(sessionName string) []int {
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", tmux.PaneTarget(sessionName), "-F", "#{pane_pid}").Output()
	if err != nil {
		// No such session is the ordinary case: nothing to kill.
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil && pid > 1 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// signalAll sends sig to each pid. Shelled out rather than done with
// syscall.Kill so the package still compiles for Windows, where the whole
// tmux-backed feature is inapplicable anyway.
func signalAll(pids []int, sig string) {
	if len(pids) == 0 {
		return
	}
	args := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 1 {
			args = append(args, strconv.Itoa(pid))
		}
	}
	if len(args) == 0 {
		return
	}
	// One call: a pid that has already exited must not stop the rest from being
	// signalled, and `kill` carries on past its own failures.
	cmd := fmt.Sprintf("kill -%s %s 2>/dev/null", sig, strings.Join(args, " "))
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		// Exit 1 only means some of them were already gone.
		log.InfoLog.Printf("dev stack: %s returned %v", cmd, err)
	}
}

func anyAlive(pids []int) bool {
	return countAlive(pids) > 0
}

func countAlive(pids []int) int {
	return len(livePIDs(pids))
}
