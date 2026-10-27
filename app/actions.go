package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"github.com/AlexanderWeismannn/adroit/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// syncSelection points the panes and the menu at the selected session. Reads
// cached state only -- no captures, no git -- so it is safe to run on every
// preview tick, where the expensive half has already been done off the loop.
func (m *home) syncSelection() {
	// selected may be nil
	selected := m.list.GetSelectedInstance()
	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	m.menu.SetInstance(selected)
	m.menu.SetAttention(m.list.HasAttention())

	// The stack is per repository, so what `d` would do here depends on which
	// row the cursor is on. Pointed on every selection sync rather than only on
	// a move: the list also changes under the cursor as sessions are added,
	// killed and restored.
	repo := ""
	if selected != nil {
		repo = selected.RepoPath()
	}
	m.devStack.Point(repo)
	m.menu.SetDevEnabled(m.devStack.Configured())
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
// syncDevBadge pushes the stack's current verdict onto the row that owns it.
func (m *home) syncDevBadge() {
	status := m.devStack.Snapshot()
	// The menu advertises the stop key off this, so it has to be told even when
	// there is nothing to badge.
	m.menu.SetDevRunning(status.Running)

	title, state := status.Title, status.State()
	if title == "" {
		state = dev.LampDown
	}
	m.list.SetDevStack(title, state)
	// The tab carries the same verdict, which is what answers the question from
	// a tab other than Run: the list badge only marks the row that owns it.
	m.tabbedWindow.SetDevStack(title, state)
}

// stopDevStack tears the development stack down, wherever it is running.
//
// Deliberately not scoped to the selected session: there is one stack, and the
// row the cursor is on has no bearing on it. Nor is it confirmed -- unlike
// killing a session, stopping a stack destroys no work and costs one keypress
// to undo.
func (m *home) stopDevStack() tea.Cmd {
	if !m.devStack.Configured() || m.devStack.RunningFor() == "" {
		return nil
	}

	// Cleared before the stop rather than after: the record exists so the next
	// run can adopt a stack that is still up, and a crash midway through a
	// teardown must not leave it pointing at one that is on its way out.
	if err := m.appState.SetDevStackInstance(""); err != nil {
		log.WarningLog.Printf("could not clear the dev stack session: %v", err)
	}

	stack := m.devStack
	return func() tea.Msg {
		return devStackStoppedMsg{err: stack.Stop()}
	}
}

// startDevStack points the development stack at the selected session.
//
// The work happens off the update loop and takes seconds to tens of seconds: it
// waits for the previous stack's ports to be released and for shared services to
// answer. Blocking the loop for that would freeze the interface over exactly the
// interval the user most wants to watch.
func (m *home) startDevStack() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Status == session.Loading {
		return nil
	}
	// Asked of the selected session's repository rather than of the config as a
	// whole: a stack defined for another repository is not one that can run here,
	// and saying "no dev stack is configured" to someone who has three would be a
	// lie.
	repoPath := selected.RepoPath()
	if cfg := m.appConfig.DevFor(repoPath); cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return m.handleError(fmt.Errorf(
			"no dev stack is configured for %s: add an entry under \"dev_by_repo\" in the Adroit config",
			filepath.Base(filepath.Clean(repoPath))))
	}
	if selected.Status == session.Paused {
		return m.handleError(fmt.Errorf("session %s is paused: resume it before running its stack", selected.Title))
	}

	dir := selected.DevDir()
	if dir == "" {
		return m.handleError(fmt.Errorf("session %s has no directory to run a stack in", selected.Title))
	}

	title := selected.Title

	// Recorded before the start rather than after: the tmux session outlives this
	// process, so a crash between launching it and writing the record would leave
	// a stack running that the next run cannot attribute to anything.
	if err := m.appState.SetDevStackInstance(title); err != nil {
		log.WarningLog.Printf("could not record the dev stack session: %v", err)
	}
	m.list.SetDevStack(title, dev.LampPending)
	m.menu.SetDevRunning(true)

	// Switch to the tab that shows what is happening. The whole point of the key
	// is watching the stack come up, and it takes long enough that landing on the
	// agent's output instead reads as nothing having happened.
	m.tabbedWindow.SelectRunTab()

	stack := m.devStack
	return func() tea.Msg {
		return devStackStartedMsg{title: title, err: stack.Start(title, dir, repoPath)}
	}
}

// syncNaming tells the list a session is being named, and what branch the name
// typed so far would create.
//
// Computed here rather than in the renderer because it needs the loaded config
// -- the prefix and the case policy -- and the renderer runs every frame.
func (m *home) syncNaming(on bool) {
	if !on {
		m.list.SetNaming(false, "")
		return
	}
	title := ""
	if n := m.list.NumInstances(); n > 0 {
		title = m.list.GetInstances()[n-1].Title
	}
	m.list.SetNaming(true, git.PreviewBranchName(
		m.appConfig.BranchPrefixFor(m.namingRepo), m.appConfig.PreserveBranchCaseFor(m.namingRepo), title))
}

func (m *home) instanceChanged() tea.Cmd {
	m.syncSelection()
	m.resetPaneHash()
	selected := m.list.GetSelectedInstance()

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateRun(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

// spinnerNeeded reports whether anything on screen is animating.
//
// The spinner used to re-tick forever, so an interface with nothing moving in it
// still re-rendered twelve times a second for the life of the process. Stopping
// the chain when nothing spins means an idle list renders only when something
// actually changes; the metadata tick re-evaluates this twice a second, so the
// animation restarts within a tick of a session picking work back up.
func (m *home) spinnerNeeded() bool {
	if m.instanceStarting || m.list.Busy() {
		return true
	}
	for _, inst := range m.list.GetInstances() {
		if inst.Status == session.Loading {
			return true
		}
		switch inst.GetActivity() {
		case session.ActivityWorking, session.ActivityShell:
			return true
		}
	}
	return false
}

// titleForRepo names a session that runs in the repository itself, after the
// directory it runs in.
//
// Unlike a branch, a repeat is legitimate — several scratch terminals on one
// checkout is the point — so a clash takes a suffix rather than being refused.
// Which is why this cannot fail: there is always a directory, and demanding a
// typed name for a session started by pressing tab and enter was a rule nothing
// on screen had announced. Reporting it through the error box made it worse
// still: under the overlay, that reads as the key having done nothing.
func titleForRepo(path string, existing []*session.Instance) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "repo"
	}

	taken := make(map[string]bool, len(existing))
	for _, inst := range existing {
		taken[inst.Title] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
}

// titleCollision refuses a name whose tmux session another session already has.
//
// Compared by tmux name, not by title: tmux drops whitespace and turns "." and
// ":" into "_", so "fix login" and "fixlogin" are one tmux session. Two rows on
// one session attach to the same agent, and killing either kills both.
func titleCollision(inst *session.Instance, existing []*session.Instance) error {
	want := tmux.SessionNameFor(inst.Title)
	wantTerm := tmux.SessionNameFor(ui.TerminalSessionName(inst.Title))
	for _, other := range existing {
		if other == inst || other.Title == "" {
			continue
		}
		// Each session also owns a Terminal-tab session, "term_" + its title, so
		// "term_x" beside "x" would put one row's agent in the other's terminal.
		if want == tmux.SessionNameFor(ui.TerminalSessionName(other.Title)) ||
			wantTerm == tmux.SessionNameFor(other.Title) {
			return fmt.Errorf("%q clashes with the session %q: one's agent would share the other's Terminal tab",
				inst.Title, other.Title)
		}
		if tmux.SessionNameFor(other.Title) == want {
			if other.Title == inst.Title {
				return fmt.Errorf("a session named %q already exists", inst.Title)
			}
			return fmt.Errorf("%q is too close to the existing session %q: both become tmux session %s",
				inst.Title, other.Title, want)
		}
	}
	return nil
}

// titleForBranch names a session after the branch it is opening.
//
// The branch name is used verbatim: the 32-character cap on the name prompt is a
// typing convenience, not a constraint on the value -- the list truncates a long
// title for display already -- and a title that is exactly the branch is what
// makes the duplicate check below mean "you already have a session on this
// branch" rather than "you happened to pick the same words".
func titleForBranch(branch string, existing []*session.Instance) (string, error) {
	if branch == "" {
		return "", fmt.Errorf("type a name for the session, or pick an existing branch to take its name from")
	}
	// Title is the storage key and the tmux session name, so a collision would
	// otherwise surface much later as "tmux session already exists".
	for _, inst := range existing {
		if tmux.SessionNameFor(inst.Title) == tmux.SessionNameFor(branch) {
			return "", fmt.Errorf("a session for branch %q already exists", branch)
		}
	}
	return branch, nil
}

// killDetail says what a kill of this session throws away, from what the row
// already knows -- nothing is computed here, on the loop. The bare "Kill
// session?" said none of it, though the kill deletes the worktree with whatever
// is uncommitted in it and, for a branch Adroit created, the branch too.
func killDetail(inst *session.Instance) string {
	if inst.NoWorktree() {
		return "It runs in the repository itself, so only the session goes; your checkout is left alone."
	}
	switch inst.GetCIStatus().State {
	case ci.StateMerged:
		return fmt.Sprintf("PR #%d is merged, so this is safe to remove.", inst.GetCIStatus().PRNumber)
	case ci.StateClosed:
		return fmt.Sprintf("PR #%d was closed without merging.", inst.GetCIStatus().PRNumber)
	}
	var parts []string
	if st := inst.GetDiffStats(); st != nil && st.Error == nil && !st.IsEmpty() {
		parts = append(parts, fmt.Sprintf("+%d −%d against its base", st.Added, st.Removed))
	}
	switch up := inst.GetUpstreamStatus(); {
	case up.Tracked() && up.Ahead > 0:
		parts = append(parts, fmt.Sprintf("%d commit(s) not pushed", up.Ahead))
	case !up.Tracked() && !up.FetchedAt.IsZero():
		parts = append(parts, "the branch was never pushed")
	}
	if len(parts) == 0 {
		return "The worktree will be removed; R brings the conversation back."
	}
	return strings.Join(parts, ", ") + ". The worktree goes with it; R brings back the conversation, not the code."
}

// updateFromRemote brings the selected session's worktree up to date with the
// branch as it stands on the remote, behind a confirmation.
//
// A worktree is created once and never moves again on its own, so a session
// opened to review a branch someone is still pushing to reads an older and older
// copy of it. This is the way out, and the badge on the row is what asks for it.
//
// The confirmation is not ceremony: a fast-forward lands somebody else's commits
// in a tree an agent may be working in, and the diverged case discards commits
// outright. Both are stated in the prompt, counted, before anything happens.
func (m *home) updateFromRemote() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected == nil || selected.Status == session.Loading {
		return nil
	}
	if !m.appConfig.UpstreamStatusEnabled() {
		return m.handleError(fmt.Errorf(`upstream tracking is off — remove "upstream_status": false from the config to use u`))
	}
	// The user's own checkout, shared with every other session on the repo and
	// possibly holding uncommitted work. Moving it from a list of sessions would
	// be action at a distance.
	if selected.NoWorktree() {
		return m.handleError(fmt.Errorf("%q runs in the repo itself — pull it yourself", selected.Title))
	}

	status := selected.GetUpstreamStatus()
	switch {
	case !status.Tracked():
		return m.handleError(fmt.Errorf("%q has no branch on the remote yet — push it first with p", selected.Title))
	case status.Diverged():
		// A force-push upstream, in the ordinary case: no merge can reconcile the
		// two histories, so the only way to the remote's version is to abandon
		// ours. Named in full, since it is the one action here that loses work.
		message := fmt.Sprintf("[!] %s diverged: %d commit(s) here are not on it.\nReset '%s' to the remote and discard them?",
			status.Ref, status.Ahead, selected.Title)
		return m.confirmAction(message, func() tea.Msg {
			return runUpdateMsg{instance: selected, reset: true}
		})
	case !status.Stale():
		return m.handleError(fmt.Errorf("%q is already up to date with %s", selected.Title, status.Ref))
	default:
		message := fmt.Sprintf("[!] Pull %d new commit(s) from %s into '%s'?",
			status.Behind, status.Ref, selected.Title)
		return m.confirmAction(message, func() tea.Msg {
			return runUpdateMsg{instance: selected}
		})
	}
}

// runUpdateCmd performs the fetch and the fast-forward (or reset) off the event
// loop.
//
// It re-reads the counts from the freshly fetched refs rather than trusting the
// badge that prompted it: the badge is up to a minute old, and a merge decided
// from a stale count is exactly the mistake this feature exists to prevent.
func runUpdateCmd(msg runUpdateMsg) tea.Cmd {
	return func() tea.Msg {
		worktree, err := msg.instance.GetGitWorktree()
		if err != nil {
			return updateDoneMsg{err: err}
		}
		repoPath, worktreePath := worktree.GetRepoPath(), worktree.GetWorktreePath()
		if msg.reset {
			_, err = upstream.Reset(repoPath, worktreePath, msg.instance.Branch)
		} else {
			_, err = upstream.Pull(repoPath, worktreePath, msg.instance.Branch)
		}
		if err != nil {
			return updateDoneMsg{err: fmt.Errorf("could not update %q: %w", msg.instance.Title, err)}
		}
		return updateDoneMsg{}
	}
}

// killApproved performs the half of a kill that has to happen on the update
// loop, and returns the half that must not.
//
// The split is between what changes the model and what waits on another
// process. Recording the session for restore, dropping its terminal pane, its
// storage row and its row in the list are all model writes, and a Cmd's
// goroutine may not make them. Stopping the dev stack and Instance.Kill are the
// opposite: the first waits out SIGTERM and then the ports, the second shells
// out to tmux kill-session and git worktree remove. Those three ran here until
// now, and a kill held every keystroke and every frame for as long as they
// took -- indefinitely, when a git lock was held elsewhere.
//
// Taking the row out first is also what makes the keypress feel answered: the
// session disappears immediately, and its remains are cleared up behind it.
func (m *home) killApproved(instance *session.Instance) tea.Cmd {
	// Removed before anything else, so the list is right even if a later step
	// fails. Remove hands back the instance it dropped rather than leaving us to
	// look it up again from a selection that has already moved on.
	removed := m.list.Remove()
	if removed == nil {
		return nil
	}
	if removed.Title != instance.Title {
		// The selection cannot move while the confirmation is up, so this should be
		// unreachable; tear down what was actually removed if it ever is not.
		log.WarningLog.Printf("kill confirmed for %q but %q was selected; removing %q",
			instance.Title, removed.Title, removed.Title)
	}
	title := removed.Title

	// Before anything is torn down: what makes this session restorable is its
	// Claude transcript, and the worktree path that finds it is about to stop
	// existing.
	m.recordKilledSession(removed)

	// Clean up terminal session for this instance
	m.tabbedWindow.CleanupTerminalForInstance(title)

	if err := m.storage.DeleteInstance(title); err != nil {
		return m.handleError(err)
	}

	// A stack outlives the app but must not outlive the session it was running
	// for: its worktree is about to be removed out from under it, and it would go
	// on holding the ports with nothing left to serve. The badge and the stored
	// name are settled here; the stop itself goes below, with the other waiting.
	stopStack := m.devStack.RunningFor() == title
	if stopStack {
		if err := m.appState.SetDevStackInstance(""); err != nil {
			log.WarningLog.Printf("could not clear the dev stack session: %v", err)
		}
		m.syncDevBadge()
	}

	return tea.Batch(
		m.instanceChanged(),
		func() tea.Msg {
			// Ordered: the stack has to let go of the worktree before git can remove
			// it.
			if stopStack {
				if err := m.devStack.Stop(); err != nil {
					log.WarningLog.Printf("could not stop the dev stack: %v", err)
				}
			}

			if err := removed.Kill(); err != nil {
				return killFinishedMsg{
					title: title,
					err:   fmt.Errorf("%q is gone from the list but did not tear down cleanly: %w", title, err),
				}
			}
			return killFinishedMsg{title: title}
		},
	)
}

// ciStatusFor returns the cached CI verdict for an instance's branch. The lookup
// is non-blocking by contract (see ci.Get) because it runs inside the same
// metadata tick as the git diffs, which only re-schedules once every goroutine
// has finished.
func ciStatusFor(instance *session.Instance) ci.Status {
	// A session with no branch of its own has no pull request to report, and
	// resolving the repository's current HEAD would attribute somebody else's
	// branch to it.
	if instance.NoWorktree() {
		return ci.Status{}
	}
	worktree, err := instance.GetGitWorktree()
	if err != nil {
		return ci.Status{}
	}
	// The recorded branch is only the fallback: ci resolves the worktree's actual
	// HEAD, so a session whose branch was renamed or switched still finds its PR.
	return ci.Get(worktree.GetRepoPath(), worktree.GetWorktreePath(), instance.Branch)
}

// upstreamStatusFor returns the cached standing of an instance's branch against
// its remote counterpart. Non-blocking by the same contract as ciStatusFor, and
// for the same reason: it shares the tick that computes the diffs.
func upstreamStatusFor(instance *session.Instance) upstream.Status {
	// A session running in the repository itself is measured too, against
	// whatever its checkout has on HEAD. The row is not offered the key that
	// moves it -- that checkout is the user's, shared with every other session on
	// the repo -- but "your master is ten commits behind" is worth knowing, and
	// this is the only place it would be said.
	if instance.NoWorktree() {
		return upstream.Get(instance.Path, instance.Path, "")
	}
	worktree, err := instance.GetGitWorktree()
	if err != nil {
		return upstream.Status{}
	}
	return upstream.Get(worktree.GetRepoPath(), worktree.GetWorktreePath(), instance.Branch)
}
