package app

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/dev"
	"github.com/AlexanderWeismannn/adroit/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// The pane tick keeps whichever pane is on screen live: it samples the tmux
// pane behind the active tab off the event loop, and hands a capture back only
// when it differs from what the pane is already showing.
//
// It used to serve the Preview tab alone. On every other tab it fired a bare
// message ten times a second, and Update answered each one by capturing the
// Terminal or Run pane ON the loop -- a tmux fork per tick in the one goroutine
// that must never wait -- and by re-rendering the Diff tab, where nothing had
// changed. Measured on an idle list: 12 renders a second on those tabs against
// 3 on Preview, and 3-4ms of every tick on the Terminal tab spent inside Update.
//
// Now one tick serves every tab, the same way: capture off the loop, compare,
// report only a change. The tabs that sample nothing -- Diff, a paused or
// unstarted session, scroll mode -- get a slow heartbeat whose only job is to
// notice that the state has changed and sampling should start again.
const (
	// paneIntervalLive is the sampling interval while the pane is changing.
	paneIntervalLive = 100 * time.Millisecond
	// paneIntervalSettled and paneIntervalIdle are the intervals after the pane
	// has held still for paneSettledAfter and paneIdleAfter respectively. A
	// session waiting for its next prompt is most of a list most of the time,
	// and sampling it ten times a second forks tmux ten times a second for a
	// capture that is identical every time.
	paneIntervalSettled = 250 * time.Millisecond
	paneSettledAfter    = 2 * time.Second
	paneIntervalIdle    = 500 * time.Millisecond
	paneIdleAfter       = 10 * time.Second
	// paneFloor is how long a sampling tick runs without reporting before it
	// returns anyway, so the loop can re-read which tab and session to sample.
	// It reports nothing when it does -- an unchanged pane is not news.
	paneFloor = 2 * time.Second
	// paneHeartbeat is the interval on a tab with nothing to sample.
	paneHeartbeat = time.Second
)

// paneTickMsg carries a capture back to the loop, with the tab and session it
// was taken for so a result that arrives after the user has moved on is dropped
// rather than painted into the wrong pane.
type paneTickMsg struct {
	tab        int
	instance   *session.Instance
	content    string
	hasContent bool
	hash       uint64
	// runStatus accompanies a Run tab capture: the lamps are state the pane
	// draws alongside the output, and they change without the output changing.
	runStatus dev.Status
	err       error
}

// paneSample is one capture of whatever the active tab shows.
type paneSample struct {
	content   string
	runStatus dev.Status
}

// hash fingerprints a sample. FNV-1a because the only question asked of it is
// "same as last time", over a few tens of kilobytes, a few times a second.
func (s paneSample) hash() uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.content))
	// The lamps are part of what the Run pane shows, so a lamp turning green
	// with the output unchanged still has to be reported.
	_, _ = fmt.Fprintf(h, "\x00%v", s.runStatus)
	return h.Sum64()
}

// paneSampler is what one tick captures: nil when the active tab has nothing
// to sample.
type paneSampler func() (paneSample, bool, error)

// paneSampler picks the capture for the active tab in its current state.
//
// The Terminal and Run samplers never create what they sample. The Terminal
// tab opens its shell on the loop when the tab is switched to, through
// instanceChanged, and the sampler only reads a shell that is already open.
func (m *home) paneSampler() (tab int, sample paneSampler) {
	selected := m.list.GetSelectedInstance()
	tab = m.tabbedWindow.GetActiveTab()
	switch tab {
	case ui.PreviewTab:
		if selected == nil || !selected.Started() || selected.Status == session.Paused ||
			m.tabbedWindow.IsPreviewInScrollMode() {
			return tab, nil
		}
		return tab, func() (paneSample, bool, error) {
			// Re-checked at sample time: a checkout that lands while the tick
			// sleeps turns Preview into an empty string, and reporting that
			// would blank the paused notice UpdateContent has just drawn.
			if !selected.Started() || selected.Status == session.Paused {
				return paneSample{}, false, nil
			}
			content, err := selected.Preview()
			return paneSample{content: content}, err == nil, err
		}
	case ui.TerminalTab:
		if selected == nil || m.tabbedWindow.IsTerminalInScrollMode() {
			return tab, nil
		}
		return tab, func() (paneSample, bool, error) {
			content, ok, err := m.tabbedWindow.SampleTerminal(selected)
			return paneSample{content: content}, ok, err
		}
	case ui.RunTab:
		if m.tabbedWindow.IsRunInScrollMode() {
			return tab, nil
		}
		return tab, func() (paneSample, bool, error) {
			content, status, ok, err := m.tabbedWindow.SampleRun()
			return paneSample{content: content, runStatus: status}, ok, err
		}
	}
	return tab, nil
}

// paneInterval is how long to wait before the next sample, given how long the
// pane has held still.
func paneInterval(quiet time.Duration) time.Duration {
	switch {
	case quiet >= paneIdleAfter:
		return paneIntervalIdle
	case quiet >= paneSettledAfter:
		return paneIntervalSettled
	}
	return paneIntervalLive
}

// paneTickCmd samples until the pane differs from what it is showing, then
// hands the capture back. With nothing to sample it is the heartbeat.
//
// lastChange is when the pane last differed, which sets the pace: a pane that
// has held still for a while is sampled less often, and the first change puts
// it back to the live rate.
func paneTickCmd(tab int, selected *session.Instance, sample paneSampler, lastHash uint64, lastChange time.Time) tea.Cmd {
	return func() tea.Msg {
		if sample == nil {
			time.Sleep(paneHeartbeat)
			return paneTickMsg{tab: tab, instance: selected}
		}
		deadline := time.Now().Add(paneFloor)
		for {
			time.Sleep(paneInterval(time.Since(lastChange)))
			s, ok, err := sample()
			if err != nil {
				return paneTickMsg{tab: tab, instance: selected, err: err}
			}
			if !ok {
				return paneTickMsg{tab: tab, instance: selected}
			}
			if h := s.hash(); h != lastHash {
				return paneTickMsg{tab: tab, instance: selected, content: s.content,
					hasContent: true, hash: h, runStatus: s.runStatus}
			}
			if time.Now().After(deadline) {
				return paneTickMsg{tab: tab, instance: selected}
			}
		}
	}
}

// paneTick builds the next tick from the current state of the interface.
func (m *home) paneTick() tea.Cmd {
	tab, sample := m.paneSampler()
	return paneTickCmd(tab, m.list.GetSelectedInstance(), sample, m.paneHash, m.paneChanged)
}

// applyPaneTick paints a capture into the pane it was taken for, if that pane
// is still the one on screen.
func (m *home) applyPaneTick(msg paneTickMsg) tea.Cmd {
	if msg.err != nil {
		// The ordinary path reports it in the pane.
		return m.instanceChanged()
	}
	if !msg.hasContent || msg.tab != m.tabbedWindow.GetActiveTab() || msg.instance != m.list.GetSelectedInstance() {
		return nil
	}
	if msg.hash != m.paneHash {
		m.paneChanged = time.Now()
	}
	m.paneHash = msg.hash
	switch msg.tab {
	case ui.PreviewTab:
		// The same guard on arrival: the session may have been parked between
		// the sample and now.
		if !msg.instance.Started() || msg.instance.Status == session.Paused {
			return nil
		}
		m.tabbedWindow.ApplyPreviewCapture(msg.instance, msg.content)
	case ui.TerminalTab:
		m.tabbedWindow.ApplyTerminalSample(msg.instance, msg.content)
	case ui.RunTab:
		m.tabbedWindow.ApplyRunSample(msg.content, msg.runStatus)
	}
	// The panes that read cached state follow the capture.
	m.syncSelection()
	return nil
}

// resetPaneHash forgets what the pane was showing, so the next sample is
// reported whatever it holds. Called wherever the pane's content was just set
// on the loop -- a selection or tab change -- so that the tick's "unchanged"
// is measured against what is actually on screen.
func (m *home) resetPaneHash() {
	m.paneHash = 0
	m.paneChanged = time.Now()
}
