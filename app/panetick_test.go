package app

import (
	"testing"
	"time"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// A pane that has held still is sampled less often, and the first change puts
// it back to the live rate. Ten captures a second of an identical pane was a
// tmux fork ten times a second for every idle session the cursor rested on.
func TestPaneSamplingSlowsAsThePaneHoldsStill(t *testing.T) {
	require.Equal(t, paneIntervalLive, paneInterval(0))
	require.Equal(t, paneIntervalLive, paneInterval(paneSettledAfter-time.Millisecond))
	require.Equal(t, paneIntervalSettled, paneInterval(paneSettledAfter))
	require.Equal(t, paneIntervalIdle, paneInterval(paneIdleAfter))
}

// A capture is for the tab and session it was taken on. One that lands after
// the user has moved on -- another row, another tab -- must be dropped, or it
// is painted into the wrong pane under the wrong name.
func TestAStaleCaptureIsDropped(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	a, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	b, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(a)()
	h.list.AddInstance(b)()
	h.list.SetSelectedInstance(1)

	// Taken for a, while b is selected.
	h.applyPaneTick(paneTickMsg{tab: ui.PreviewTab, instance: a, content: "a's pane", hasContent: true, hash: 7})
	require.Zero(t, h.paneHash, "nothing was applied")

	// Taken on the Preview tab, while the Diff tab is showing.
	h.tabbedWindow.Toggle()
	require.True(t, h.tabbedWindow.IsInDiffTab())
	h.applyPaneTick(paneTickMsg{tab: ui.PreviewTab, instance: b, content: "b's pane", hasContent: true, hash: 9})
	require.Zero(t, h.paneHash)
}

// The Diff tab has nothing to sample and a paused session has no pane, so the
// tick there is a heartbeat and not a sampler; on the Preview tab of a running
// session it samples.
func TestOnlyALivePaneGetsASampler(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	_, sample := h.paneSampler()
	require.Nil(t, sample, "no session, nothing to sample")

	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)()
	inst.SetStatus(session.Paused)
	_, sample = h.paneSampler()
	require.Nil(t, sample, "a paused session has no pane")

	h.tabbedWindow.Toggle() // Diff
	_, sample = h.paneSampler()
	require.Nil(t, sample, "the Diff tab is not sampled")
}

// A row with an action in flight keeps the animation alive, so the mark it
// wears from the keypress actually moves.
func TestABusyRowKeepsTheSpinnerRunning(t *testing.T) {
	h := newDevStackHome(t, dev.New(nil), &fakeAppState{})
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	h.list.AddInstance(inst)()
	inst.SetStatus(session.Paused)
	require.False(t, h.spinnerNeeded())

	h.offLoop(inst, "resuming", func() tea.Msg { return nil })
	require.True(t, h.spinnerNeeded())
	require.True(t, h.spinnerRunning, "the chain is restarted by the keypress, not the next tick")
}
