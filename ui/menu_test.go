package ui

import (
	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

func newMenuInstance(t *testing.T, status ci.Status) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "session", Path: ".", Program: "echo"})
	require.NoError(t, err)
	inst.SetCIStatus(status)
	return inst
}

// The menu is the answer to "can I open a PR from here, and with what key". It
// has to say nothing when there is nothing to open, or it advertises a key whose
// only outcome is an error.
func TestMenuOffersOpenPROnlyWithAPullRequest(t *testing.T) {
	tests := []struct {
		name   string
		status ci.Status
		want   bool
	}{
		{"an open pull request", ci.Status{State: ci.StateSuccess, PRNumber: 4420}, true},
		{"a failing one is still openable", ci.Status{State: ci.StateFailure, PRNumber: 4420}, true},
		{"a merged one is still openable", ci.Status{State: ci.StateMerged, PRNumber: 4418}, true},
		{"no pull request", ci.Status{State: ci.StateNoPR}, false},
		// Also what a disabled badge and a lookup that has not returned produce.
		{"no verdict yet", ci.Status{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMenu()
			m.SetSize(200, 1)
			m.SetInstance(newMenuInstance(t, tt.status))

			require.Equal(t, tt.want, hasOption(m, keys.KeyOpenPR))
			require.Equal(t, tt.want, strings.Contains(m.String(), "open PR"))
		})
	}
}

// The CI lookup runs in the background, so a session's pull request becomes known
// after it was selected. The menu must pick that up without another SetInstance.
func TestMenuPicksUpAPullRequestFoundAfterSelection(t *testing.T) {
	inst := newMenuInstance(t, ci.Status{})
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(inst)
	require.NotContains(t, m.String(), "open PR")

	inst.SetCIStatus(ci.Status{State: ci.StateSuccess, PRNumber: 4420})
	require.Contains(t, m.String(), "open PR")
}

// The group boundaries used to be a fixed table while the options were
// conditional, so any variable-length group put the separator in the wrong place
// and coloured the wrong keys. Assert they track the options actually built.
func TestMenuGroupsTrackTheOptions(t *testing.T) {
	for _, withPR := range []bool{false, true} {
		status := ci.Status{}
		if withPR {
			status = ci.Status{State: ci.StateSuccess, PRNumber: 4420}
		}

		for _, tab := range []int{PreviewTab, DiffTab} {
			m := NewMenu()
			m.SetSize(200, 1)
			m.SetInstance(newMenuInstance(t, status))
			m.SetActiveTab(tab)
			m.updateOptions()

			require.Equal(t, len(m.options), m.groups[len(m.groups)-1].end,
				"withPR=%v tab=%d: groups do not cover every option", withPR, tab)

			action := m.groups[m.actionGroup]
			for _, k := range []keys.KeyName{keys.KeyEnter, keys.KeySubmit} {
				require.True(t, inRange(m, k, action), "withPR=%v tab=%d: %v left the action group", withPR, tab, k)
			}
			if withPR {
				require.True(t, inRange(m, keys.KeyOpenPR, action),
					"tab=%d: the open-PR key must be an action, not a system key", tab)
			}
			if tab == DiffTab {
				require.True(t, inRange(m, keys.KeyShiftUp, action),
					"withPR=%v: the scroll key must be an action, not a system key", withPR)
			}
		}
	}
}

func hasOption(m *Menu, want keys.KeyName) bool {
	m.updateOptions()
	for _, k := range m.options {
		if k == want {
			return true
		}
	}
	return false
}

func inRange(m *Menu, want keys.KeyName, g menuGroup) bool {
	for i, k := range m.options {
		if k == want {
			return i >= g.start && i < g.end
		}
	}
	return false
}

// Both overlay menus advertised nothing but "enter submit name", which on the
// prompt overlay is not even true: enter acts only from the button, and the
// branch picker behind tab is the reason that overlay exists.
func TestOverlayMenusNameTheirKeys(t *testing.T) {
	tests := []struct {
		name  string
		state MenuState
		want  []string
	}{
		{"naming a session", StateNewInstance, []string{"tab", "pick branch", "esc", "cancel"}},
		{"the prompt overlay", StatePrompt, []string{"tab", "next field", "ctrl+s", "submit", "esc", "cancel"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMenu()
			m.SetSize(200, 1)
			m.SetState(tt.state)
			rendered := m.String()
			for _, want := range tt.want {
				require.Contains(t, rendered, want)
			}
		})
	}
}

// The prompt overlay's enter does not submit a name — it confirms the form.
func TestPromptMenuDoesNotClaimToSubmitAName(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetState(StatePrompt)
	require.NotContains(t, m.String(), "submit name")
}

// A session running in the repo itself has no branch and no worktree, so push
// and checkout have nothing to act on. Offering them would advertise keys whose
// only outcome is an error.
func TestMenuHidesWorktreeActionsForARepoSession(t *testing.T) {
	plain, err := session.NewInstance(session.InstanceOptions{
		Title: "scratch", Path: ".", Program: "echo", NoWorktree: true,
	})
	require.NoError(t, err)

	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(plain)
	rendered := m.String()

	require.NotContains(t, rendered, " push")
	require.NotContains(t, rendered, "checkout")
	// What it can still do.
	require.Contains(t, rendered, "open")
	require.Contains(t, rendered, "kill")

	require.False(t, hasOption(m, keys.KeySubmit))
	require.False(t, hasOption(m, keys.KeyCheckout))
	require.True(t, hasOption(m, keys.KeyEnter))
	require.False(t, hasOption(m, keys.KeyResume), "it is not paused")

	// The groups are computed from the options actually built, so dropping two
	// must not leave a separator mid-group or colour the wrong keys.
	require.Equal(t, len(m.options), m.groups[len(m.groups)-1].end)
	require.True(t, inRange(m, keys.KeyEnter, m.groups[m.actionGroup]))
}

// Resume is the one worktree-shaped action a repo session does need. It is parked
// as Paused when its tmux session could not be restored (a reboot, a crash,
// `tmux kill-server`), and without the key that row can only be killed: attach
// refuses a paused instance.
func TestMenuOffersResumeForAPausedRepoSession(t *testing.T) {
	paused, err := session.NewInstance(session.InstanceOptions{
		Title: "scratch", Path: ".", Program: "echo", NoWorktree: true,
	})
	require.NoError(t, err)
	paused.SetStatus(session.Paused)

	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(paused)
	rendered := m.String()

	require.True(t, hasOption(m, keys.KeyResume))
	require.Contains(t, rendered, "resume")
	// Still nothing to push and nothing to check out.
	require.False(t, hasOption(m, keys.KeySubmit))
	require.False(t, hasOption(m, keys.KeyCheckout))

	require.Equal(t, len(m.options), m.groups[len(m.groups)-1].end)
	require.True(t, inRange(m, keys.KeyResume, m.groups[m.actionGroup]))
}

// The key is only worth advertising where it can do something: the menu is a
// promise, and a "run dev stack" entry that can only report "nothing configured"
// is a worse answer than no entry.
func TestMenuOffersTheDevKeyOnlyWhenAStackIsConfigured(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(newDevMenuInstance(t, false))

	require.False(t, hasOption(m, keys.KeyDev), "unconfigured, the key must not be offered")
	require.NotContains(t, m.String(), "d run")

	m.SetDevEnabled(true)
	require.True(t, hasOption(m, keys.KeyDev))
	require.Contains(t, m.String(), "d run")
}

// Both shapes of session have somewhere to run: a worktree session in its
// worktree, and a worktree-less one in the repository itself -- which is also
// the one tree guaranteed to be provisioned.
func TestMenuOffersTheDevKeyForBothSessionShapes(t *testing.T) {
	for _, noWorktree := range []bool{false, true} {
		m := NewMenu()
		m.SetSize(200, 1)
		m.SetDevEnabled(true)
		m.SetInstance(newDevMenuInstance(t, noWorktree))
		require.True(t, hasOption(m, keys.KeyDev), "noWorktree=%v", noWorktree)
	}
}

// A session with nowhere to run must not advertise the key. An instance that has
// never been started has no worktree yet, which is exactly that case.
func TestMenuHidesTheDevKeyForASessionWithNoDirectory(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetDevEnabled(true)
	m.SetInstance(newMenuInstance(t, ci.Status{}))

	require.Empty(t, newMenuInstance(t, ci.Status{}).DevDir(), "precondition")
	require.False(t, hasOption(m, keys.KeyDev))
}

// newDevMenuInstance builds a started session that has a directory to run in.
func newDevMenuInstance(t *testing.T, noWorktree bool) *session.Instance {
	t.Helper()
	dir := t.TempDir()
	data := session.InstanceData{
		Title:      "session",
		Path:       dir,
		Status:     session.Paused,
		NoWorktree: noWorktree,
		Program:    "claude",
	}
	if !noWorktree {
		data.Worktree = session.GitWorktreeData{
			RepoPath:     dir,
			WorktreePath: dir,
			BranchName:   "feature",
			SessionName:  "session",
		}
	}
	inst, err := session.FromInstanceData(data)
	require.NoError(t, err)
	require.NotEmpty(t, inst.DevDir())
	return inst
}

// Stopping is only worth advertising while something is running, and then on
// every row: there is one stack, and it belongs to no particular session.
func TestMenuOffersTheStopKeyOnlyWhileAStackRuns(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetDevEnabled(true)
	m.SetInstance(newDevMenuInstance(t, false))

	require.False(t, hasOption(m, keys.KeyDevStop), "nothing running, nothing to stop")

	m.SetDevRunning(true)
	require.True(t, hasOption(m, keys.KeyDevStop))
	require.Contains(t, m.String(), "x stop")

	m.SetDevRunning(false)
	require.False(t, hasOption(m, keys.KeyDevStop))
}

// A stack outlives the sessions in the list -- it is adopted by title, and the
// session that owned it may since have been killed. With no entry here the only
// way to reach the stop key would be to create a session first.
func TestMenuOffersTheStopKeyWithNoSessionsAtAll(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetState(StateEmpty)
	m.SetDevEnabled(true)

	require.False(t, hasOption(m, keys.KeyDevStop))

	m.SetDevRunning(true)
	require.True(t, hasOption(m, keys.KeyDevStop))
	require.Contains(t, m.String(), "x stop")

	// And back off again. The empty menu is built by splicing into a shared
	// package-level slice, so an append that aliased it would leave the key in
	// place for the rest of the process.
	m.SetDevRunning(false)
	require.False(t, hasOption(m, keys.KeyDevStop))
	require.NotContains(t, m.String(), "stop dev stack")
}

// The empty menu's groups are a fixed pair, so inserting a key into it is
// exactly the shape that used to put the separator in the wrong place.
func TestEmptyMenuGroupsTrackTheStopKey(t *testing.T) {
	m := NewMenu()
	m.SetSize(200, 1)
	m.SetState(StateEmpty)
	m.SetDevEnabled(true)
	m.SetDevRunning(true)

	require.Equal(t, len(m.options), m.groups[len(m.groups)-1].end,
		"the last group must reach the end of the options")
	for i, g := range m.groups {
		require.LessOrEqual(t, g.start, g.end, "group %d is inverted", i)
		require.LessOrEqual(t, g.end, len(m.options), "group %d overruns the options", i)
	}
}

// The menu sets the width of the whole view: lipgloss.Place pads but never
// truncates, so a menu wider than the terminal both ran off the right edge and
// stretched the view, pushing the session list sideways behind an empty gutter.
// It must fit whatever it is given.
func TestTheMenuNeverExceedsItsWidth(t *testing.T) {
	for _, width := range []int{200, 160, 130, 110, 95, 70, 50, 34, 20} {
		m := NewMenu()
		m.SetDevEnabled(true)
		m.SetDevRunning(true)
		m.SetInstance(newDevMenuInstance(t, false))
		m.SetSize(width, 1)

		for _, line := range strings.Split(m.String(), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width,
				"width %d: %q overruns", width, line)
		}
	}
}

// Keys are never dropped, only their glosses: a key you cannot see is
// unreachable, where a key without its description is merely unexplained.
func TestTheMenuKeepsEveryKeyAsItAbbreviates(t *testing.T) {
	m := NewMenu()
	m.SetDevEnabled(true)
	m.SetDevRunning(true)
	m.SetInstance(newDevMenuInstance(t, false))

	m.SetSize(200, 1)
	full := ansiCodes.ReplaceAllString(m.String(), "")
	require.Contains(t, full, "q quit", "precondition: the widest form spells everything out")

	for _, width := range []int{110, 70, 50} {
		m.SetSize(width, 1)
		rendered := ansiCodes.ReplaceAllString(m.String(), "")
		for _, k := range []string{"n", "D", "p", "r", "d", "x", "t", "q"} {
			require.Contains(t, rendered, k, "width %d dropped the %q key entirely", width, k)
		}
	}
}

// The contextual actions keep their words longest -- they are the ones that
// change with the selection, so they are the ones worth explaining.
func TestTheMenuGivesUpSystemDescriptionsFirst(t *testing.T) {
	m := NewMenu()
	m.SetDevEnabled(true)
	m.SetDevRunning(true)
	m.SetInstance(newDevMenuInstance(t, false))

	// One column short of what the full menu needs, whatever that happens to be:
	// pinning a literal width would only be testing the current label lengths.
	m.SetSize(0, 1)
	full := lipgloss.Width(strings.TrimSpace(ansiCodes.ReplaceAllString(m.String(), "")))
	m.SetSize(full-1, 1)

	rendered := ansiCodes.ReplaceAllString(m.String(), "")
	require.Contains(t, rendered, "p push", "an action keeps its description")
	require.NotContains(t, rendered, "q quit", "a system key gives its description up first")
	require.Contains(t, rendered, "q", "but the key itself stays")
}

// The update key is the prompt as much as the action: a row is only told it can
// be updated when there is something on the remote to update from. Ahead-only
// must not offer it -- there is nothing to pull, and pushing is p.
func TestMenuOffersUpdateOnlyWhenBehindTheRemote(t *testing.T) {
	tests := []struct {
		name   string
		status upstream.Status
		want   bool
	}{
		{"behind", upstream.Status{Ref: "origin/review", Behind: 3}, true},
		{"diverged", upstream.Status{Ref: "origin/review", Behind: 3, Ahead: 1}, true},
		{"level with the remote", upstream.Status{Ref: "origin/review"}, false},
		{"only ahead", upstream.Status{Ref: "origin/review", Ahead: 2}, false},
		{"no remote branch", upstream.Status{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := session.NewInstance(session.InstanceOptions{Title: "session", Path: ".", Program: "echo"})
			require.NoError(t, err)
			inst.SetUpstreamStatus(tt.status)

			m := NewMenu()
			m.SetSize(200, 1)
			m.SetInstance(inst)

			require.Equal(t, tt.want, hasOption(m, keys.KeyUpdate))
			require.Equal(t, tt.want, strings.Contains(m.String(), "update"))
			require.Equal(t, len(m.options), m.groups[len(m.groups)-1].end)
		})
	}
}

// A session running in the repository itself shares the user's own checkout,
// which this key has no business moving.
func TestMenuHidesUpdateForARepoSession(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{
		Title: "scratch", Path: ".", Program: "echo", NoWorktree: true,
	})
	require.NoError(t, err)
	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/main", Behind: 4})

	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(inst)

	require.False(t, hasOption(m, keys.KeyUpdate))
}

// The lookup completes in the background, after the row was selected, so the key
// has to appear without another SetInstance -- the same requirement the pull
// request key has.
func TestMenuPicksUpStalenessFoundAfterSelection(t *testing.T) {
	inst, err := session.NewInstance(session.InstanceOptions{Title: "session", Path: ".", Program: "echo"})
	require.NoError(t, err)

	m := NewMenu()
	m.SetSize(200, 1)
	m.SetInstance(inst)
	require.NotContains(t, m.String(), "update")

	inst.SetUpstreamStatus(upstream.Status{Ref: "origin/review", Behind: 1})
	require.Contains(t, m.String(), "update")
}
