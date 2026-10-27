package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/session/upstream"

	tea "github.com/charmbracelet/bubbletea"
)

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		currentDir, _ := os.Getwd()
		branches, err := git.SearchBranches(currentDir, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance *session.Instance
	// pane is one capture's worth of everything the session's pane says.
	pane tmux.PaneState
	// diffStats is nil both when there was nothing to compute and when the tick
	// deliberately skipped the computation, which diffComputed distinguishes: a
	// skipped diff must leave the last good counts on the row rather than blank
	// them.
	diffStats    *git.DiffStats
	diffComputed bool
	diffContent  bool
	// diffUntracked records that this diff folded untracked files in, which the
	// instance stamps so the walk is not paid for again immediately.
	diffUntracked bool
	ciStatus      ci.Status
	upstream      upstream.Status
	// currentBranch is "" when the worktree's HEAD could not be resolved, which
	// the instance reads as "keep the recorded name". Only meaningful when
	// branchResolved is set, for the same reason as diffComputed.
	currentBranch  string
	branchResolved bool
}

// metadataPlan is what one instance's slot in a tick is allowed to do.
//
// Decided on the main thread, where the instance's timestamps can be read
// without racing the renderer, and handed to the goroutine that does the work.
type metadataPlan struct {
	instance *session.Instance
	// diffDue is true when the diff is old enough to recompute regardless of
	// whether the pane changed.
	diffDue bool
	// diffAllowed is true when enough time has passed to honour a changed pane.
	diffAllowed bool
	// stageUntracked is true when this diff should also pay for the untracked
	// walk.
	stageUntracked bool
	// wantContent is true when the Diff tab is showing this session, which is the
	// only time the full diff text is worth loading.
	wantContent bool
	// branchDue is true when the checked-out branch is worth re-reading.
	branchDue bool
}

const (
	// diffMaxAgeSelected and diffMaxAgeOther are the floors under the
	// pane-changed gate: how long a diff can go unrecomputed when nothing has
	// been drawn to the session's pane at all. Only a background shell writing
	// with nothing on screen to show for it gets this far.
	diffMaxAgeSelected = 4 * time.Second
	diffMaxAgeOther    = 10 * time.Second
	// diffCooldownSelected and diffCooldownOther are the ceilings over the same
	// gate: the fastest a changing pane can ask for a new diff.
	//
	// Necessary because "the pane changed" is a much weaker signal than it looks:
	// an agent mid-turn redraws continuously -- its own spinner is enough -- so
	// the gate is open on every tick for exactly the sessions that are busy, and
	// without a ceiling those sessions pay the full twice-a-second cost the gate
	// was meant to avoid. Measured on a live list: the gate alone left ~72ms/sec
	// on the table.
	//
	// The selected row is the one being read; the rest are decoration on a line
	// that is mostly branch name.
	diffCooldownSelected = time.Second
	diffCooldownOther    = 3 * time.Second
	// untrackedMaxAge is how often a diff pays for the `add -N` walk that folds
	// untracked files into the counts -- 12.1ms measured, more than the diff
	// itself. A file appearing untracked is rare next to the rate of this tick.
	untrackedMaxAge = 15 * time.Second
	// branchMaxAge is how long a resolved branch name is trusted. Renaming or
	// switching a branch inside a worktree is legitimate but rare, and the name
	// is only used for display.
	branchMaxAge = 30 * time.Second
)

// offLoop runs work that shells out to git and tmux as a Cmd, off the update
// loop, marking the session busy until its message comes back. A session that
// is already busy is left alone: the press is answered, not queued.
func (m *home) offLoop(instance *session.Instance, doing string, work func() tea.Msg) tea.Cmd {
	if m.busy[instance] {
		return m.handleNotice(fmt.Sprintf("still %s %s", doing, instance.Title))
	}
	if m.busy == nil {
		m.busy = map[*session.Instance]bool{}
	}
	m.busy[instance] = true
	// The row says so too, from this keypress: the notice is a line at the
	// bottom of the screen, and the eye is on the row the key was pressed on.
	m.list.SetBusy(instance, doing)
	cmds := []tea.Cmd{m.handleNotice(fmt.Sprintf("%s %s…", doing, instance.Title)), work}
	if !m.spinnerRunning {
		m.spinnerRunning = true
		cmds = append(cmds, m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// planMetadata decides, on the main thread, what the next tick is allowed to
// spend on each session.
func (m *home) planMetadata(active []*session.Instance) []metadataPlan {
	selected := m.list.GetSelectedInstance()
	// The full diff text is only ever rendered by the diff pane. Computed for the
	// selected session on every tick regardless of which tab was open, it was a
	// whole diff loaded into memory twice a second to be thrown away.
	diffTabOpen := m.tabbedWindow.IsInDiffTab()

	plans := make([]metadataPlan, 0, len(active))
	for _, inst := range active {
		maxAge, cooldown := diffMaxAgeOther, diffCooldownOther
		wantContent := false
		if inst == selected {
			maxAge, cooldown = diffMaxAgeSelected, diffCooldownSelected
			wantContent = diffTabOpen
		}
		plans = append(plans, metadataPlan{
			instance:       inst,
			diffDue:        inst.DiffStale(maxAge, wantContent),
			diffAllowed:    inst.DiffStale(cooldown, wantContent),
			wantContent:    wantContent,
			stageUntracked: inst.UntrackedStale(untrackedMaxAge),
			branchDue:      inst.BranchStale(branchMaxAge),
		})
	}
	return plans
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(plans []metadataPlan, ciEnabled, upstreamEnabled bool, stack *dev.Stack) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		// Detached, not waited on. Every readiness probe is a network operation
		// with a timeout, and on this goroutine a hung one held up the diff and
		// status of every session in the list; the stack writes its own state and
		// the interface reads it on the next render. Poll paces itself and
		// refuses to overlap, so calling it every tick costs nothing once the
		// lamps are green.
		go stack.Poll()

		if len(plans) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(plans))
		var wg sync.WaitGroup
		for idx, plan := range plans {
			wg.Add(1)
			go func(i int, plan metadataPlan) {
				defer wg.Done()
				instance := plan.instance
				r := &results[i]
				r.instance = instance
				r.pane = instance.PollPane()

				// The capture above is the cheap half of this tick, and it answers
				// the expensive half's question: a session whose pane has not
				// changed has almost certainly not written to disk either, so the
				// diff it produced last time still stands. plan.diffDue is the
				// floor under that inference.
				if plan.diffDue || (r.pane.Updated && plan.diffAllowed) {
					r.diffComputed = true
					r.diffContent = plan.wantContent
					r.diffUntracked = plan.stageUntracked
					if plan.wantContent {
						r.diffStats = instance.ComputeDiff()
					} else {
						r.diffStats = instance.ComputeDiffNumstat(plan.stageUntracked)
					}
				}
				if ciEnabled {
					r.ciStatus = ciStatusFor(instance)
				}
				if upstreamEnabled {
					r.upstream = upstreamStatusFor(instance)
				}
				if plan.branchDue {
					r.branchResolved = true
					r.currentBranch = instance.ResolveCurrentBranch()
				}
			}(idx, plan)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}
