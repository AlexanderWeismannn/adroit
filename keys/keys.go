package keys

import (
	"charm.land/bubbles/v2/key"
)

type KeyName int

const (
	KeyUp KeyName = iota
	KeyDown
	KeyEnter
	KeyNew
	KeyKill
	KeyQuit
	KeyReview
	KeyPush
	KeySubmit

	KeyTab        // Tab is a special keybinding for switching between panes.
	KeySubmitName // SubmitName is a special keybinding for submitting the name of a new instance.

	// Menu-only labels. These name what a key does in one particular overlay,
	// where the global binding's own description would be wrong -- tab does not
	// "switch tab" while naming a session. Deliberately absent from
	// GlobalKeyStringsMap: they are captions, not a second binding for a key that
	// is already handled, and registering them would make the menu highlight the
	// wrong entry.
	KeyPickBranch // tab, on the name prompt: choose an existing branch
	KeyNextField  // tab, in an overlay: move to the next field
	KeyConfirm    // ctrl+s, in an overlay: submit the form from any field
	KeyCancel     // esc: back out of the overlay

	KeyCheckout
	KeyResume
	KeyPrompt        // New key for entering a prompt
	KeyHelp          // Key for showing help screen
	KeyOpenPR        // Key for opening the selected session's pull request in a browser
	KeyTheme         // Key for opening the colour theme picker
	KeyUpdate        // Key for pulling the remote's new commits into the selected session
	KeyDev           // Key for running the development stack on the selected session
	KeyDevStop       // Key for stopping the development stack, wherever it is running
	KeyRestore       // Key for restoring the conversation of a killed session
	KeyNextAttention // Key for jumping to the next session waiting on you
	KeySendPrompt    // Key for sending a prompt to a running session without attaching
	KeyPageUp        // Key for scrolling the active pane up a page
	KeyPageDown      // Key for scrolling the active pane down a page
	KeySettings      // Key for opening agents, keys and workspace settings

	// Diff keybindings
	KeyShiftUp
	KeyShiftDown

	// Reorder keybindings
	KeyMoveUp
	KeyMoveDown

	// KeyLeft and KeyRight switch tabs.
	KeyLeft
	KeyRight
	// KeyMode cycles the layout mode; KeyPrevSession and KeyNextSession move
	// the selection without leaving the window.
	KeyMode
	KeyPrevSession
	KeyNextSession

	// Captions for the bar while the window owns the keyboard: what the arrow
	// keys do there, and the way back to the list.
	KeyPaneScroll
	KeyPanePage
	KeyPaneFollow
	KeyPaneTabs
	KeyPaneAttach
	KeyPaneList
	KeyPaneSession
	KeyPaneHelp
	KeyPaneQuit
)

// Group is where a binding is drawn in the menu.
type Group int

const (
	// GroupNone is a key the menu never advertises: moving the cursor, scrolling
	// by a line, reordering rows. The help card still lists them.
	GroupNone Group = iota
	// GroupSession acts on the selected session: attach, prompt, resume,
	// checkout, push, kill, and the jump to a session waiting on you.
	GroupSession
	// GroupTools acts on its branch, the dev stack and the window: update, the
	// pull request, run and stop the stack, scroll.
	GroupTools
	// GroupApp acts on the interface: tab, new, restore, settings, theme, help,
	// quit.
	GroupApp
	// GroupNaming is the row while a new session is being named.
	GroupNaming
	// GroupPrompt is the prompt overlay.
	GroupPrompt
	// GroupOverlay is both of the above: the key that backs out of either.
	GroupOverlay
	// GroupPane is the bar while the window owns the keyboard.
	GroupPane
)

// Ctx is what a binding's predicates see: the facts the menu knows, never the
// model. Every field is a plain fact about the list, the selected session and
// the interface, so the table can be read -- and tested -- without either.
type Ctx struct {
	// HasInstance is whether a session is selected at all.
	HasInstance bool
	// Loading, Paused, Started and NoWorktree describe the selected session.
	Loading, Paused, Started, NoWorktree bool
	// PRDone is whether its pull request is merged or closed: the session is
	// done with, and killing it leads.
	PRDone bool
	// CanOpenPR is whether a pull request is known to exist; CanUpdate whether
	// the remote has commits the worktree does not.
	CanOpenPR, CanUpdate bool
	// DevConfigured is whether a dev stack is configured for the selected
	// session's repository (or at all, with no session); DevDir whether the
	// session has a directory to run one in; DevRunning whether one is up.
	DevConfigured, DevDir, DevRunning bool
	// Restorable is whether a killed session can be brought back; Attention
	// whether any session is waiting on you.
	Restorable, Attention bool
	// ScrollableTab is whether the window shows a tab that scrolls by a line
	// (diff, terminal, run); RunTab whether it is the Run tab.
	ScrollableTab, RunTab bool
}

// A Binding is one key the interface answers to: how it is pressed, how it is
// shown, where it sits in the menu and when it is offered. The menu, the help
// card and the overlay menus all render from this one table, so a key cannot
// be advertised in one place and missing from another.
type Binding struct {
	Name KeyName
	// Keys are the strings bubbletea delivers for it. Nil for a caption: a name
	// the menu shows for a key that an overlay handles itself (tab does not
	// "switch tab" while a session is being named).
	Keys []string
	// Key is how it is shown; Label what it does, in the menu's words; Help the
	// same in the help card's, where there is room for a sentence.
	Key, Label, Help string
	Group            Group
	// When says whether the key is offered now. Nil means always.
	When func(Ctx) bool
	// Dim says whether the key is drawn dim after the actions: present on this
	// row, but with nothing to do right now -- open PR on a branch whose pull
	// request is not known yet, update on a branch level with its remote. A key
	// that simply vanished read as a bug ("where did g go?"); a dim one says the
	// key exists and is waiting on something. Nil means never.
	Dim func(Ctx) bool
	// Lead moves the key ahead of its group's other entries: a session whose
	// pull request is merged is done with, and killing it is the one thing left
	// to do, so that key leads rather than trailing new. Nil means never.
	Lead func(Ctx) bool
}

func always(Ctx) bool { return true }

// selected is a session that is there and has finished starting: the state in
// which its keys can act on it.
func selected(c Ctx) bool { return c.HasInstance && !c.Loading }

// Table is every key, in the order the menu and the help card show them.
var Table = []Binding{
	{Name: KeyUp, Keys: []string{"up", "k"}, Key: "↑/k", Label: "up"},
	{Name: KeyDown, Keys: []string{"down", "j"}, Key: "↓/j", Label: "down"},
	{Name: KeyShiftDown, Keys: []string{"shift+down"}, Key: "shift+↓", Label: "scroll"},
	{Name: KeyMoveUp, Keys: []string{"K"}, Key: "K", Label: "move up"},
	{Name: KeyMoveDown, Keys: []string{"J"}, Key: "J", Label: "move down"},
	{Name: KeyPageUp, Keys: []string{"pgup"}, Key: "pgup", Label: "page up"},
	{Name: KeyPageDown, Keys: []string{"pgdown"}, Key: "pgdn", Label: "page down"},
	{Name: KeyLeft, Keys: []string{"left", "h"}, Key: "←/h", Label: "previous tab"},
	{Name: KeyRight, Keys: []string{"right", "l"}, Key: "→/l", Label: "next tab"},
	{Name: KeyPrevSession, Keys: []string{"<"}, Key: "<", Label: "previous session"},
	{Name: KeyNextSession, Keys: []string{">"}, Key: ">", Label: "next session"},

	// -- the session --

	// The jump comes first when it is offered: it is the one entry that says
	// something about the list rather than the row.
	{Name: KeyNextAttention, Keys: []string{"]"}, Key: "]", Label: "waiting", Group: GroupSession,
		Help: "Jump to the next session waiting on you, then the next unseen finish",
		When: func(c Ctx) bool { return selected(c) && c.Attention }, Lead: always},
	// Enter does nothing on a paused row (except on the Run tab, where it
	// attaches to the stack), so it is not offered there: resume is.
	{Name: KeyEnter, Keys: []string{"enter", "o"}, Key: "↵", Label: "attach", Group: GroupSession,
		Help: "Attach to the selected session",
		When: func(c Ctx) bool { return selected(c) && (!c.Paused || c.RunTab) }},
	{Name: KeySendPrompt, Keys: []string{"i"}, Key: "i", Label: "prompt", Group: GroupSession,
		Help: "Send a prompt to the selected session without attaching",
		When: func(c Ctx) bool { return selected(c) && c.Started && !c.Paused }},
	// Resume and checkout are each other's dim twin: a running session shows
	// resume dim, a paused one checkout, so the pair reads as one switch rather
	// than a key that comes and goes. A session running in the repository
	// itself has no worktree to check out, so it gets neither.
	{Name: KeyResume, Keys: []string{"r"}, Key: "r", Label: "resume", Group: GroupSession,
		Help: "Resume a paused session",
		When: func(c Ctx) bool { return selected(c) && c.Paused },
		Dim:  func(c Ctx) bool { return selected(c) && !c.Paused && !c.NoWorktree }},
	{Name: KeyCheckout, Keys: []string{"c"}, Key: "c", Label: "checkout", Group: GroupSession,
		Help: "Checkout: commit changes and pause session",
		When: func(c Ctx) bool { return selected(c) && !c.NoWorktree && !c.Paused },
		Dim:  func(c Ctx) bool { return selected(c) && !c.NoWorktree && c.Paused }},
	// A session running in the repository itself has no branch of its own, so
	// push has nothing to act on -- offering it would advertise a key whose
	// only outcome is an error.
	{Name: KeySubmit, Keys: []string{"p"}, Key: "p", Label: "push", Group: GroupSession,
		Help: "Commit and push branch to github",
		When: func(c Ctx) bool { return selected(c) && !c.NoWorktree }},
	{Name: KeyKill, Keys: []string{"D"}, Key: "D", Label: "kill", Group: GroupSession,
		Help: "Kill (delete) the selected session",
		When: selected, Lead: func(c Ctx) bool { return c.PRDone }},

	// -- the branch, the stack and the window --

	// Offered only when there is something to bring in, which makes the key the
	// prompt as well as the action: a row wearing the behind or diverged badge is
	// also the only row that advertises the key that clears it. Ahead-only does
	// not count -- there is nothing to update FROM, and pushing is p.
	{Name: KeyUpdate, Keys: []string{"u"}, Key: "u", Label: "update", Group: GroupTools,
		When: func(c Ctx) bool { return selected(c) && c.CanUpdate },
		Dim:  func(c Ctx) bool { return selected(c) && !c.NoWorktree && !c.Paused && !c.CanUpdate }},
	// Offered only for a session whose pull request we know exists, so the menu
	// answers "can I open one from here" as well as "which key opens it". A row
	// with no pull request, or with the badge switched off, shows the key dim
	// -- the alternative is advertising a key that reports a failure. Only a
	// branch of the session's own is dimmed: on a repo session the key could
	// never apply, and dimming it would promise one.
	{Name: KeyOpenPR, Keys: []string{"g"}, Key: "g", Label: "PR", Group: GroupTools,
		Help: "Open the session's pull request in your browser",
		When: func(c Ctx) bool { return selected(c) && c.CanOpenPR },
		Dim:  func(c Ctx) bool { return selected(c) && !c.NoWorktree && !c.CanOpenPR }},
	// Offered only when a stack is configured at all. With no "dev" block the key
	// can do nothing but explain itself, and a menu entry is a promise.
	{Name: KeyDev, Keys: []string{"d"}, Key: "d", Label: "dev", Group: GroupTools,
		Help: "Run the dev stack here, moving it off another session",
		When: func(c Ctx) bool { return selected(c) && c.DevConfigured && c.DevDir }},
	// Stopping is offered whenever a stack is up, on every row rather than only
	// the one that owns it -- and with no row at all: a stack is adopted from a
	// previous run by title, and the session that owned it may since have been
	// killed, so without this the only way to reach it would be to create a
	// session first.
	{Name: KeyDevStop, Keys: []string{"x"}, Key: "x", Label: "stop", Group: GroupTools,
		Help: "Stop it, wherever it runs. The Run tab shows its state.",
		When: func(c Ctx) bool { return c.DevConfigured && c.DevRunning && !c.Loading }},
	// Scrolling by a line is advertised on the tabs that scroll.
	{Name: KeyShiftUp, Keys: []string{"shift+up"}, Key: "shift+↑", Label: "scroll", Group: GroupTools,
		When: func(c Ctx) bool { return selected(c) && c.ScrollableTab }},

	// -- the interface --

	{Name: KeyTab, Keys: []string{"tab"}, Key: "tab", Label: "pane", Group: GroupApp,
		Help: "Give the keyboard to the window, and back to the list",
		When: selected},
	{Name: KeyNew, Keys: []string{"n"}, Key: "n", Label: "new", Group: GroupApp,
		Help: "Create a new session"},
	{Name: KeyPrompt, Keys: []string{"N"}, Key: "N", Label: "new+prompt", Group: GroupApp,
		Help: "Create a new session with a prompt",
		When: func(c Ctx) bool { return !c.HasInstance }},
	// Restore is offered in the empty state too, on purpose: killing the last
	// session is exactly when it is wanted.
	{Name: KeyRestore, Keys: []string{"R"}, Key: "R", Label: "restore", Group: GroupApp,
		Help: "Restore a killed session's conversation in a fresh workspace",
		When: func(c Ctx) bool { return c.Restorable && !c.Loading }},
	{Name: KeyMode, Keys: []string{"m"}, Key: "m", Label: "mode", Group: GroupApp,
		Help: "Cycle the layout: compact, split, wide (remembered for this width)"},
	{Name: KeySettings, Keys: []string{"s"}, Key: "s", Label: "settings", Group: GroupApp,
		Help: "Settings: agents and their API keys, workspaces, options"},
	{Name: KeyTheme, Keys: []string{"t"}, Key: "t", Label: "theme", Group: GroupApp,
		Help: "Change the colour theme (previews as you move)"},
	{Name: KeyHelp, Keys: []string{"?"}, Key: "?", Label: "help", Group: GroupApp},
	{Name: KeyQuit, Keys: []string{"q"}, Key: "q", Label: "quit", Group: GroupApp,
		Help: "Quit the application"},

	// Captions for the overlays' own keys. Both overlay menus used to advertise
	// nothing but "enter submit name", which on the prompt overlay is not even
	// true -- enter submits only from the button, and the branch picker it hides
	// behind tab was the whole reason the overlay existed.
	{Name: KeySubmitName, Key: "enter", Label: "submit name", Group: GroupNaming},
	{Name: KeyPickBranch, Key: "tab", Label: "pick branch", Group: GroupNaming},
	{Name: KeyNextField, Key: "tab", Label: "next field", Group: GroupPrompt},
	{Name: KeyConfirm, Key: "ctrl+s", Label: "submit", Group: GroupPrompt},
	{Name: KeyCancel, Key: "esc", Label: "cancel", Group: GroupOverlay},

	// The bar while the window owns the keyboard.
	{Name: KeyPaneScroll, Key: "↑↓", Label: "scroll", Group: GroupPane},
	{Name: KeyPanePage, Key: "pgup/dn", Label: "page", Group: GroupPane},
	{Name: KeyPaneFollow, Key: "esc", Label: "follow", Group: GroupPane},
	{Name: KeyPaneTabs, Key: "←→", Label: "tab", Group: GroupPane},
	{Name: KeyPaneAttach, Key: "↵", Label: "attach", Group: GroupPane},
	{Name: KeyPaneList, Key: "tab", Label: "list", Group: GroupPane},
	{Name: KeyPaneSession, Key: "<>", Label: "session", Group: GroupPane},
	{Name: KeyPaneHelp, Key: "?", Label: "help", Group: GroupPane},
	{Name: KeyPaneQuit, Key: "q", Label: "quit", Group: GroupPane},
}

// Lookup is the table by name.
var Lookup = func() map[KeyName]*Binding {
	m := make(map[KeyName]*Binding, len(Table))
	for i := range Table {
		m[Table[i].Name] = &Table[i]
	}
	return m
}()

// GlobalKeyStringsMap maps what bubbletea delivers to the key's name. Captions
// are deliberately absent: they name keys that are already handled, and
// registering them would make the menu highlight the wrong entry.
var GlobalKeyStringsMap = func() map[string]KeyName {
	m := map[string]KeyName{}
	for _, b := range Table {
		for _, k := range b.Keys {
			m[k] = b.Name
		}
	}
	return m
}()

// GlobalkeyBindings is the table as bubbles bindings, which is what the menu
// and the panes render.
var GlobalkeyBindings = func() map[KeyName]key.Binding {
	m := make(map[KeyName]key.Binding, len(Table))
	for _, b := range Table {
		m[b.Name] = key.NewBinding(key.WithKeys(b.Keys...), key.WithHelp(b.Key, b.Label))
	}
	return m
}()

// Offered is what the menu shows for ctx: the session group, the tools group
// and the interface group, each in table order with the leading keys first.
// A key that is offered dim is included; Dimmed says which.
func Offered(ctx Ctx) (session, tools, app []KeyName) {
	pick := func(g Group) []KeyName {
		var lead, rest []KeyName
		for _, b := range Table {
			if b.Group != g {
				continue
			}
			offered := b.When == nil || b.When(ctx)
			dim := b.Dim != nil && b.Dim(ctx)
			if !offered && !dim {
				continue
			}
			if b.Lead != nil && b.Lead(ctx) {
				lead = append(lead, b.Name)
			} else {
				rest = append(rest, b.Name)
			}
		}
		return append(lead, rest...)
	}
	return pick(GroupSession), pick(GroupTools), pick(GroupApp)
}

// Dimmed is the keys drawn dim for ctx, in table order.
func Dimmed(ctx Ctx) []KeyName {
	var out []KeyName
	for _, b := range Table {
		if b.Dim != nil && b.Dim(ctx) {
			out = append(out, b.Name)
		}
	}
	return out
}

// OverlayMenu is what the menu shows while an overlay owns the keys: its own
// captions, then the key that backs out of it.
func OverlayMenu(g Group) []KeyName {
	var out []KeyName
	for _, b := range Table {
		if b.Group == g || b.Group == GroupOverlay {
			out = append(out, b.Name)
		}
	}
	return out
}

// PaneMenu is the bar while the window owns the keyboard: the arrows' meaning
// there, then the tabs and attach, then the way back and out.
func PaneMenu() []KeyName {
	var out []KeyName
	for _, b := range Table {
		if b.Group == GroupPane {
			out = append(out, b.Name)
		}
	}
	return out
}

// A HelpLine is one row of the help card: a key from the table, named by Name,
// or a literal pair for the keys the card groups into one row (↑/k and ↓/j) and
// the ones no binding owns (1-9, ctrl-q to detach).
type HelpLine struct {
	Name      KeyName
	Key, Desc string
}

// Text is the key and the description the line shows: the binding's where it
// names one, its own otherwise.
func (l HelpLine) Text() (key, desc string) {
	if l.Key != "" || l.Desc != "" {
		return l.Key, l.Desc
	}
	b := Lookup[l.Name]
	return b.Key, b.Help
}

// A HelpSection is one heading of the help card. Legend marks the section the
// interface fills with its own badge legend, built beside the glyphs it
// documents so it cannot drift from the rows it describes.
type HelpSection struct {
	Title  string
	Lines  []HelpLine
	Legend bool
}

// HelpSections is the help card, in order.
var HelpSections = []HelpSection{
	{Title: "Managing:", Lines: []HelpLine{
		{Name: KeyNew},
		{Name: KeyPrompt},
		{Key: "tab", Desc: "At the name prompt: pick an existing branch, or no branch at all"},
		{Name: KeyKill},
		{Name: KeyRestore},
		{Key: "↑/k, ↓/j", Desc: "Navigate between sessions"},
		{Key: "1-9", Desc: "Select the session with that number"},
		{Name: KeyNextAttention},
		{Key: "J/K", Desc: "Reorder sessions"},
		{Name: KeyEnter},
		{Name: KeySendPrompt},
		{Key: "ctrl-q", Desc: "Detach from session"},
	}},
	{Title: "Handoff:", Lines: []HelpLine{
		{Name: KeySubmit},
		{Name: KeyCheckout},
		{Name: KeyResume},
		{Name: KeyOpenPR},
	}},
	{Title: "Running the project:", Lines: []HelpLine{
		{Name: KeyDev},
		{Name: KeyDevStop},
	}},
	{Title: "Status & badges:", Legend: true},
	{Title: "Other:", Lines: []HelpLine{
		{Name: KeySettings},
		{Name: KeyTheme},
		{Name: KeyTab},
		{Key: "←/h, →/l", Desc: "Switch between preview, diff, terminal and run tabs"},
		{Key: "<, >", Desc: "Previous or next session, without leaving the window"},
		{Name: KeyMode},
		{Key: "shift-↓/↑", Desc: "Scroll in preview/diff/terminal/run view"},
		{Key: "pgup/pgdn", Desc: "Scroll a page at a time; esc returns to the live view"},
		{Name: KeyQuit},
	}},
}
