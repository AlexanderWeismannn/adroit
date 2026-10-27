package ui

import (
	"strings"

	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/theme"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
)

var keyStyle, descStyle, sepStyle lipgloss.Style

func init() { theme.OnChange(applyMenuTheme) }

func applyMenuTheme() {
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent))
	descStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	sepStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
}

const (
	entrySeparator = "  "
	groupSeparator = "  ·  "
)

// MenuState represents different states the menu can be in
type MenuState int

const (
	StateDefault MenuState = iota
	StateEmpty
	StateNewInstance
	StatePrompt
)

// menuGroup is a half-open range of option indices rendered as one group, with a
// vertical separator after the last of them.
type menuGroup struct {
	start int
	end   int
}

type Menu struct {
	options       []keys.KeyName
	height, width int
	state         MenuState
	instance      *session.Instance
	activeTab     int
	// devEnabled is whether a dev stack is configured, which decides whether the
	// key that runs one is worth advertising. devRunning is whether one is up,
	// which decides the same for the key that stops it.
	devEnabled bool
	devRunning bool
	// restorable is whether any killed session can still be brought back, which
	// decides whether the key that brings one back is worth advertising. A menu
	// entry is a promise, and with nothing killed the key can only report that.
	restorable bool
	// attention is whether any session is waiting on an answer or has a finish
	// not yet looked at.
	attention bool

	// groups partition options for separator placement: the session's keys,
	// then the branch, stack and window keys, then the interface's. Computed
	// alongside the options rather than as a fixed table, because the options
	// are conditional and a fixed table silently mismatched every variation.
	groups []menuGroup

	// keyDown is the key which is pressed. The default is -1.
	keyDown keys.KeyName

	// dimmed are the keys this row would have if its state were different:
	// open PR on a branch whose pull request is not known yet, update on a
	// branch level with its remote, resume on a running session. They are
	// drawn in place in the separator colour, and only while the bar has its
	// full width. A key that simply vanished read as a bug -- "where did g
	// go?" -- where a dim one says the key exists and is waiting on something.
	dimmed []keys.KeyName
}

func NewMenu() *Menu {
	m := &Menu{
		state:     StateEmpty,
		activeTab: 0,
		keyDown:   -1,
	}
	m.updateOptions()
	return m
}

func (m *Menu) Keydown(name keys.KeyName) {
	m.keyDown = name
}

func (m *Menu) ClearKeydown() {
	m.keyDown = -1
}

// SetState updates the menu state and options accordingly
func (m *Menu) SetState(state MenuState) {
	m.state = state
	m.updateOptions()
}

// SetInstance updates the current instance and refreshes menu options
func (m *Menu) SetInstance(instance *session.Instance) {
	m.instance = instance
	// Only change the state if we're not in a special state (NewInstance or Prompt)
	if m.state != StateNewInstance && m.state != StatePrompt {
		if m.instance != nil {
			m.state = StateDefault
		} else {
			m.state = StateEmpty
		}
	}
	m.updateOptions()
}

// SetActiveTab updates the currently active tab
// SetDevEnabled records whether a dev stack is configured.
func (m *Menu) SetDevEnabled(enabled bool) {
	m.devEnabled = enabled
	m.updateOptions()
}

// SetAttention records whether any session is waiting on you, which is when
// the key that jumps to one is worth advertising.
func (m *Menu) SetAttention(attention bool) {
	if m.attention == attention {
		return
	}
	m.attention = attention
	m.updateOptions()
}

// SetRestorable records whether there is a killed session to restore.
func (m *Menu) SetRestorable(restorable bool) {
	if m.restorable == restorable {
		return
	}
	m.restorable = restorable
	m.updateOptions()
}

// SetDevRunning records whether a dev stack is currently up.
func (m *Menu) SetDevRunning(running bool) {
	if m.devRunning == running {
		return
	}
	m.devRunning = running
	m.updateOptions()
}

func (m *Menu) SetActiveTab(tab int) {
	m.activeTab = tab
	m.updateOptions()
}

// ctx is what the key table's predicates see: the facts this menu holds,
// reduced to the plain booleans the table is written against.
func (m *Menu) ctx() keys.Ctx {
	c := keys.Ctx{
		DevConfigured: m.devEnabled,
		DevRunning:    m.devRunning,
		Restorable:    m.restorable,
		Attention:     m.attention,
		ScrollableTab: m.activeTab == DiffTab || m.activeTab == TerminalTab || m.activeTab == RunTab,
		RunTab:        m.activeTab == RunTab,
	}
	if m.instance == nil {
		return c
	}
	c.HasInstance = true
	c.Loading = m.instance.Status == session.Loading
	c.Paused = m.instance.Status == session.Paused
	c.Started = m.instance.Started()
	c.NoWorktree = m.instance.NoWorktree()
	st := m.instance.GetCIStatus().State
	c.PRDone = st == ci.StateMerged || st == ci.StateClosed
	c.CanOpenPR = m.canOpenPR()
	c.CanUpdate = m.canUpdate()
	c.DevDir = m.instance.DevDir() != ""
	return c
}

// updateOptions derives the options from the key table for the current state.
func (m *Menu) updateOptions() {
	switch m.state {
	case StateNewInstance:
		options := keys.OverlayMenu(keys.GroupNaming)
		m.setOptions(options, []menuGroup{{0, len(options)}}, nil)
	case StatePrompt:
		options := keys.OverlayMenu(keys.GroupPrompt)
		m.setOptions(options, []menuGroup{{0, len(options)}}, nil)
	default:
		m.compose()
	}
}

// compose lays the table's groups out as the bar's: the session's keys, then
// the branch, stack and window keys, then the interface's. A group with
// nothing in it is left out rather than drawn as an empty gap.
func (m *Menu) compose() {
	ctx := m.ctx()
	var options []keys.KeyName
	var groups []menuGroup
	session, tools, app := keys.Offered(ctx)
	for _, g := range [][]keys.KeyName{session, tools, app} {
		if len(g) == 0 {
			continue
		}
		groups = append(groups, menuGroup{len(options), len(options) + len(g)})
		options = append(options, g...)
	}
	m.setOptions(options, groups, keys.Dimmed(ctx))
}

func (m *Menu) setOptions(options []keys.KeyName, groups []menuGroup, dimmed []keys.KeyName) {
	m.options = options
	m.groups = groups
	m.dimmed = dimmed
}

// Dimmed reports the keys drawn dim: present on the row, inapplicable now.
func (m *Menu) Dimmed() []keys.KeyName {
	return append([]keys.KeyName(nil), m.dimmed...)
}

// canOpenPR reports whether the selected session has a pull request to open.
//
// A number is the only honest evidence of one: it is what the lookup returned,
// and it is absent both when the branch has no pull request and when the badge
// feature is switched off, which are the two cases where the key has nothing to
// act on.
func (m *Menu) canOpenPR() bool {
	return m.instance != nil && m.instance.GetCIStatus().PRNumber > 0
}

// canUpdate reports whether the selected session's branch has commits waiting on
// the remote.
//
// A worktree is the only thing that can be updated: a session running in the
// repository itself shares the user's own checkout, and moving that under them
// from here is not this key's business.
func (m *Menu) canUpdate() bool {
	if m.instance == nil || m.instance.NoWorktree() || m.instance.Status == session.Paused {
		return false
	}
	return m.instance.GetUpstreamStatus().Stale()
}

// SetSize sets the width of the window. The menu will be centered horizontally within this width.
func (m *Menu) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// abbrev is how much of the bar is spelled out. The bar must fit the terminal
// -- it is what sets the width of the whole view, since lipgloss.Place pads but
// never truncates, so an over-wide bar both ran off the right edge and pushed
// the list sideways behind an empty gutter.
//
// Keys are never dropped, only their words, and the session's own keys keep
// theirs longest: a key you cannot see is unreachable, where a key without its
// gloss is merely unexplained -- and `?` explains all of them.
type abbrev int

const (
	abbrevNone abbrev = iota
	// abbrevDims: the dim keys go first. A key that cannot be pressed to any
	// effect is the first thing to give way when room is short.
	abbrevDims
	// abbrevApp: tab, new, settings, theme, help, quit lose their words next.
	// They are the same on every screen, so they are the ones already known.
	abbrevApp
	// abbrevTools: update, PR, dev, stop, scroll follow.
	abbrevTools
	// abbrevAll: keys only.
	abbrevAll
)

// spellsOut reports whether the option at i keeps its word at this level.
func (m *Menu) spellsOut(i int, level abbrev) bool {
	switch level {
	case abbrevNone, abbrevDims:
		return true
	case abbrevAll:
		return false
	}
	group := m.groupOf(i)
	if group < 0 || len(m.groups) == 1 {
		return true
	}
	// Groups are session, tools, app, in that order; an absent group shifts the
	// rest down, so the last group is always the app's and the one before it
	// the tools' (when there are three).
	last := len(m.groups) - 1
	if level == abbrevApp {
		return group != last
	}
	return group < last-1
}

// groupOf returns the index of the group option i belongs to, or -1.
func (m *Menu) groupOf(i int) int {
	for g, group := range m.groups {
		if i >= group.start && i < group.end {
			return g
		}
	}
	return -1
}

func (m *Menu) isDim(k keys.KeyName) bool {
	for _, d := range m.dimmed {
		if d == k {
			return true
		}
	}
	return false
}

// String draws the bar, left-aligned on its row, spelling out as much as the
// width allows.
func (m *Menu) String() string {
	// The options are re-derived here rather than only when the selection changes,
	// because one of them turns on a fact that arrives later: the CI lookup runs in
	// the background, so a session's pull request becomes known a tick or two after
	// it was selected. Deriving them is pure and cheap.
	m.updateOptions()

	rendered := m.renderAt(abbrevNone)
	for _, level := range []abbrev{abbrevDims, abbrevApp, abbrevTools, abbrevAll} {
		if m.width <= 0 || lipgloss.Width(rendered) <= m.width {
			break
		}
		rendered = m.renderAt(level)
	}
	// Even keys alone overflow a narrow enough terminal. Cut rather than let it
	// wrap: a wrapped bar takes its second row from the panes above it.
	if m.width > 0 && lipgloss.Width(rendered) > m.width {
		rendered = truncate.StringWithTail(rendered, uint(m.width), "…")
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Center, rendered)
}

// renderAt draws every entry as "key word", two spaces between entries and a
// dot between groups:
//
//	↵ attach  i prompt  r resume  c checkout  p push  D kill  ·  u update  g PR  ·  tab pane  n new  ? help  q quit
//
// A dim entry is drawn in the separator colour, in its place, and only at full
// width: a key that cannot be pressed to any effect is the first thing to give
// way when room is short.
func (m *Menu) renderAt(level abbrev) string {
	var s strings.Builder
	s.WriteString(" ")

	wrote, lastGroup := 0, -1 // entries written so far, and the group of the last one
	for i, k := range m.options {
		dim := m.isDim(k)
		if dim && level != abbrevNone {
			continue
		}
		if group := m.groupOf(i); wrote > 0 && group != lastGroup {
			s.WriteString(sepStyle.Render(groupSeparator))
		} else if wrote > 0 {
			s.WriteString(entrySeparator)
		}
		lastGroup = m.groupOf(i)
		wrote++

		binding := keys.GlobalkeyBindings[k]
		keyText, word := binding.Help().Key, binding.Help().Desc
		if !m.spellsOut(i, level) {
			word = ""
		}
		localKeyStyle, localDescStyle := keyStyle, descStyle
		if dim {
			localKeyStyle, localDescStyle = sepStyle, sepStyle
		}
		if m.keyDown == k {
			localKeyStyle = localKeyStyle.Underline(true)
			localDescStyle = localDescStyle.Underline(true)
		}
		s.WriteString(localKeyStyle.Render(keyText))
		if word != "" {
			s.WriteString(" ")
			s.WriteString(localDescStyle.Render(word))
		}
	}
	return s.String()
}
