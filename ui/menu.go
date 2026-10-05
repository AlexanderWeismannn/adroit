package ui

import (
	"github.com/AlexanderWeismannn/adroit/keys"
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/ci"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
)

var keyStyle, descStyle, sepStyle, actionGroupStyle, menuStyle lipgloss.Style

func init() { theme.OnChange(applyMenuTheme) }

func applyMenuTheme() {
	keyStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	descStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	sepStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Subtle))
	actionGroupStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
	menuStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent))
}

var separator = " • "
var verticalSeparator = " │ "

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

	// groups partition options for separator placement, and actionGroup indexes
	// the one rendered in the action colour (-1 for none).
	//
	// Computed alongside the options rather than as a fixed table, because the
	// options are conditional -- a paused session offers resume where a running
	// one offers checkout, the diff tab adds a scroll key, and a session with a
	// pull request adds the key that opens it. A fixed table silently mismatched
	// every one of those: with the scroll key present the separator landed before
	// it instead of after, and the key itself rendered in the system colour.
	groups      []menuGroup
	actionGroup int

	// keyDown is the key which is pressed. The default is -1.
	keyDown keys.KeyName
}

// Restore sits with the actions in the empty state on purpose: killing the last
// session is exactly when it is wanted, and that is the one moment when no row
// is there to carry the key.
var defaultMenuOptions = []keys.KeyName{keys.KeyNew, keys.KeyPrompt, keys.KeyRestore, keys.KeyTheme, keys.KeyHelp, keys.KeyQuit}

// Both overlay menus used to advertise nothing but "enter submit name", which on
// the prompt overlay is not even true -- enter submits only from the button, and
// the branch picker it hides behind tab was the whole reason the overlay existed.
var newInstanceMenuOptions = []keys.KeyName{keys.KeySubmitName, keys.KeyPickBranch, keys.KeyCancel}
var promptMenuOptions = []keys.KeyName{keys.KeyNextField, keys.KeyConfirm, keys.KeyCancel}

func NewMenu() *Menu {
	m := &Menu{
		options:   defaultMenuOptions,
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

// updateOptions updates the menu options based on current state and instance
func (m *Menu) updateOptions() {
	switch m.state {
	case StateEmpty:
		m.setEmptyOptions()
	case StateDefault:
		if m.instance != nil {
			// When there is an instance, show that instance's options
			m.addInstanceOptions()
		} else {
			// When there is no instance, show the empty state
			m.setEmptyOptions()
		}
	case StateNewInstance:
		// Every option in these two is an action, so the whole row is the action
		// group -- there is no navigation or system key to set them apart from.
		m.setOptions(newInstanceMenuOptions, []menuGroup{{0, len(newInstanceMenuOptions)}}, 0)
	case StatePrompt:
		m.setOptions(promptMenuOptions, []menuGroup{{0, len(promptMenuOptions)}}, 0)
	}
}

// setEmptyOptions shows the no-session menu, whose first pair are the actions.
//
// A stack can outlive every session in the list -- it is adopted from a previous
// run by title, and the session that owned it may since have been killed -- so
// the key that stops one belongs here too, or the only way to reach it would be
// to create a session first.
func (m *Menu) setEmptyOptions() {
	options := defaultMenuOptions
	actionEnd := 3
	if !m.restorable {
		options = append(append([]keys.KeyName{}, defaultMenuOptions[:2]...), defaultMenuOptions[3:]...)
		actionEnd = 2
	}
	if m.devEnabled && m.devRunning {
		options = append(append([]keys.KeyName{}, options[:actionEnd]...),
			append([]keys.KeyName{keys.KeyDevStop}, options[actionEnd:]...)...)
		actionEnd++
	}
	m.setOptions(options, []menuGroup{{0, actionEnd}, {actionEnd, len(options)}}, 0)
}

func (m *Menu) setOptions(options []keys.KeyName, groups []menuGroup, actionGroup int) {
	m.options = options
	m.groups = groups
	m.actionGroup = actionGroup
}

func (m *Menu) addInstanceOptions() {
	// Loading instances only get minimal options
	if m.instance != nil && m.instance.Status == session.Loading {
		m.setOptions([]keys.KeyName{keys.KeyNew, keys.KeyTheme, keys.KeyHelp, keys.KeyQuit},
			[]menuGroup{{0, 1}, {1, 4}}, -1)
		return
	}

	// Instance management group. The jump comes first when it is offered: it is
	// the one entry that says something about the list rather than the row.
	var options []keys.KeyName
	if m.attention {
		options = append(options, keys.KeyNextAttention)
	}
	// A session whose pull request is merged or closed is done with, and killing
	// it is the one thing left to do -- so the key leads rather than trailing new.
	if st := m.instance.GetCIStatus().State; st == ci.StateMerged || st == ci.StateClosed {
		options = append(options, keys.KeyKill, keys.KeyNew)
	} else {
		options = append(options, keys.KeyNew, keys.KeyKill)
	}
	if m.restorable {
		options = append(options, keys.KeyRestore)
	}

	// Action group. A session running in the repository itself has no branch and no
	// worktree, so push and checkout have nothing to act on -- offering them would
	// advertise keys whose only outcome is an error. Resume is different: such a
	// session is parked as Paused when its tmux session could not be restored (a
	// reboot, `tmux kill-server`), and resuming starts a fresh one in the
	// repository. Without the key that row can only be killed.
	// Enter does nothing on a paused row (except on the Run tab, where it
	// attaches to the stack), so it is not offered there: resume is.
	var actionGroup []keys.KeyName
	if m.instance.Status != session.Paused || m.activeTab == RunTab {
		actionGroup = append(actionGroup, keys.KeyEnter)
	}
	if m.instance.Started() && m.instance.Status != session.Paused {
		actionGroup = append(actionGroup, keys.KeySendPrompt)
	}
	if m.instance.NoWorktree() {
		if m.instance.Status == session.Paused {
			actionGroup = append(actionGroup, keys.KeyResume)
		}
	} else {
		actionGroup = append(actionGroup, keys.KeySubmit)
		if m.instance.Status == session.Paused {
			actionGroup = append(actionGroup, keys.KeyResume)
		} else {
			actionGroup = append(actionGroup, keys.KeyCheckout)
		}
	}

	// Offered only for a session whose pull request we know exists, so the menu
	// answers "can I open one from here" as well as "which key opens it". A row
	// with no pull request, or with the badge switched off, simply does not show
	// the key -- the alternative is advertising a key that reports a failure.
	if m.canOpenPR() {
		actionGroup = append(actionGroup, keys.KeyOpenPR)
	}

	// Offered only when there is something to bring in, which makes the key the
	// prompt as well as the action: a row wearing the behind or diverged badge is
	// also the only row that advertises the key that clears it. Ahead-only does
	// not count -- there is nothing to update FROM, and pushing is p.
	if m.canUpdate() {
		actionGroup = append(actionGroup, keys.KeyUpdate)
	}

	// Offered only when a stack is configured at all. With no "dev" block the key
	// can do nothing but explain itself, and a menu entry is a promise.
	if m.devEnabled && m.instance != nil && m.instance.DevDir() != "" {
		actionGroup = append(actionGroup, keys.KeyDev)
	}

	// Stopping is offered whenever a stack is up, on every row rather than only
	// the one that owns it: there is one stack, it belongs to no particular row,
	// and the session you happen to have the cursor on has no bearing on whether
	// you want it stopped.
	if m.devEnabled && m.devRunning {
		actionGroup = append(actionGroup, keys.KeyDevStop)
	}

	// Navigation group (when in diff tab)
	if m.activeTab == DiffTab || m.activeTab == TerminalTab || m.activeTab == RunTab {
		actionGroup = append(actionGroup, keys.KeyShiftUp)
	}

	// System group
	systemGroup := []keys.KeyName{keys.KeyTab, keys.KeyTheme, keys.KeyHelp, keys.KeyQuit}

	actionStart := len(options)
	options = append(options, actionGroup...)
	actionEnd := len(options)
	options = append(options, systemGroup...)

	m.setOptions(options,
		[]menuGroup{{0, actionStart}, {actionStart, actionEnd}, {actionEnd, len(options)}}, 1)
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

// abbrev is how much of the menu is spelled out. The menu must fit the terminal
// -- it is what sets the width of the whole view, since lipgloss.Place pads but
// never truncates, so an over-wide menu both ran off the right edge and pushed
// the list sideways behind an empty gutter.
//
// Keys are never dropped, only their descriptions, and the contextual actions
// keep theirs longest: a key you cannot see is unreachable, where a key without
// its gloss is merely unexplained -- and `?` explains all of them.
type abbrev int

const (
	abbrevNone abbrev = iota
	// abbrevSystem: tab, theme, help, quit lose their words first. They are the
	// same on every screen, so they are the ones already known.
	abbrevSystem
	// abbrevManagement: new and kill follow.
	abbrevManagement
	// abbrevAll: keys only.
	abbrevAll
)

// spellsOut reports whether the option at i keeps its description at this level.
func (m *Menu) spellsOut(i int, level abbrev) bool {
	switch level {
	case abbrevNone:
		return true
	case abbrevAll:
		return false
	}
	group := m.groupOf(i)
	if group < 0 || group == m.actionGroup {
		return true
	}
	if level == abbrevSystem {
		return group != len(m.groups)-1
	}
	return false
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

func (m *Menu) String() string {
	// The options are re-derived here rather than only when the selection changes,
	// because one of them turns on a fact that arrives later: the CI lookup runs in
	// the background, so a session's pull request becomes known a tick or two after
	// it was selected. Deriving them is pure and cheap.
	m.updateOptions()

	rendered := m.renderAt(abbrevNone)
	for _, level := range []abbrev{abbrevSystem, abbrevManagement, abbrevAll} {
		if m.width <= 0 || lipgloss.Width(rendered) <= m.width {
			break
		}
		rendered = m.renderAt(level)
	}
	// Even keys alone overflow a narrow enough terminal. Cut rather than let it
	// wrap: a wrapped menu takes its second row from the panes above it.
	if m.width > 0 && lipgloss.Width(rendered) > m.width {
		rendered = truncate.StringWithTail(rendered, uint(m.width), "…")
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, menuStyle.Render(rendered))
}

func (m *Menu) renderAt(level abbrev) string {
	var s strings.Builder

	for i, k := range m.options {
		binding := keys.GlobalkeyBindings[k]

		var (
			localActionStyle = actionGroupStyle
			localKeyStyle    = keyStyle
			localDescStyle   = descStyle
		)
		if m.keyDown == k {
			localActionStyle = localActionStyle.Underline(true)
			localKeyStyle = localKeyStyle.Underline(true)
			localDescStyle = localDescStyle.Underline(true)
		}

		inActionGroup := m.actionGroup >= 0 && m.actionGroup < len(m.groups) &&
			i >= m.groups[m.actionGroup].start && i < m.groups[m.actionGroup].end

		keyText, descText := binding.Help().Key, binding.Help().Desc
		if !m.spellsOut(i, level) {
			descText = ""
		}

		if inActionGroup {
			s.WriteString(localActionStyle.Render(keyText))
			if descText != "" {
				s.WriteString(" ")
				s.WriteString(localActionStyle.Render(descText))
			}
		} else {
			s.WriteString(localKeyStyle.Render(keyText))
			if descText != "" {
				s.WriteString(" ")
				s.WriteString(localDescStyle.Render(descText))
			}
		}

		// Add appropriate separator
		if i != len(m.options)-1 {
			isGroupEnd := false
			for _, group := range m.groups {
				if i == group.end-1 {
					s.WriteString(sepStyle.Render(verticalSeparator))
					isGroupEnd = true
					break
				}
			}
			if !isGroupEnd {
				s.WriteString(sepStyle.Render(separator))
			}
		}
	}
	return s.String()
}
