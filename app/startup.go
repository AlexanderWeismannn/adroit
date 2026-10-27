package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/ui"
)

// reapOrphanedTerminals kills the Terminal-tab shells of sessions that are gone.
//
// The pane closes a terminal when its session is killed in the same run, but a
// terminal outlives the process that made it, and nothing ever looked at the
// ones left behind. They accumulate for as long as the tmux server lives: seven
// were found on this machine, for sessions killed days earlier.
//
// Only the terminals. An agent's own session carries a live conversation and is
// deliberately left alone -- it is what pausing and resuming rely on, and a
// session missing from the list is not proof its work is finished.
func reapOrphanedTerminals(instances []*session.Instance) {
	reapOrphanedTerminalsWith(cmd.MakeExecutor(), instances)
}

func reapOrphanedTerminalsWith(exec cmd.Executor, instances []*session.Instance) {
	names, err := tmux.ListSessionNames(exec)
	if err != nil {
		log.WarningLog.Printf("could not list tmux sessions to reap terminals: %v", err)
		return
	}

	live := make(map[string]bool, len(instances))
	for _, instance := range instances {
		live[tmux.SessionNameFor(ui.TerminalSessionName(instance.Title))] = true
		// A session titled "term_x" runs its agent in adroit_term_x, which has
		// the terminals' prefix. It is never an orphaned terminal: reaping it
		// killed that agent on every launch.
		live[tmux.SessionNameFor(instance.Title)] = true
	}

	prefix := tmux.SessionNameFor(ui.TerminalSessionName(""))
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || live[name] {
			continue
		}
		if err := tmux.KillSession(exec, name); err != nil {
			log.WarningLog.Printf("could not reap orphaned terminal %s: %v", name, err)
			continue
		}
		log.InfoLog.Printf("reaped orphaned terminal session %s", name)
	}
}

// orphanedWorktrees lists the folders in the worktrees directory that no stored
// session's worktree lives in. A kill whose teardown timed out, or a crash
// part way through creating a session, leaves one behind, and nothing looked:
// two were found on this machine, one of them holding uncommitted work. They are
// reported rather than removed for exactly that reason.
func orphanedWorktrees(dir string, instances []*session.Instance) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var owned []string
	for _, instance := range instances {
		if p := instance.ToInstanceData().Worktree.WorktreePath; p != "" {
			owned = append(owned, filepath.Clean(p))
		}
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		top := filepath.Join(dir, entry.Name())
		isOwned := false
		for _, p := range owned {
			// A branch name with a slash nests the worktree a level down, so the
			// folder is owned if any session's worktree is it or lies inside it.
			if p == top || strings.HasPrefix(p, top+string(filepath.Separator)) {
				isOwned = true
				break
			}
		}
		if !isOwned {
			orphans = append(orphans, entry.Name())
		}
	}
	return orphans
}
