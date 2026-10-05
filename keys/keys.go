package keys

import (
	"github.com/charmbracelet/bubbles/key"
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
)

// GlobalKeyStringsMap is a global, immutable map string to keybinding.
var GlobalKeyStringsMap = map[string]KeyName{
	"up":         KeyUp,
	"k":          KeyUp,
	"down":       KeyDown,
	"j":          KeyDown,
	"shift+up":   KeyShiftUp,
	"shift+down": KeyShiftDown,
	"J":          KeyMoveDown,
	"K":          KeyMoveUp,
	"N":          KeyPrompt,
	"enter":      KeyEnter,
	"o":          KeyEnter,
	"n":          KeyNew,
	"D":          KeyKill,
	"q":          KeyQuit,
	"tab":        KeyTab,
	"c":          KeyCheckout,
	"r":          KeyResume,
	"g":          KeyOpenPR,
	"u":          KeyUpdate,
	"t":          KeyTheme,
	"d":          KeyDev,
	"x":          KeyDevStop,
	"R":          KeyRestore,
	"]":          KeyNextAttention,
	"i":          KeySendPrompt,
	"pgup":       KeyPageUp,
	"pgdown":     KeyPageDown,
	"s":          KeySettings,
	"p":          KeySubmit,
	"?":          KeyHelp,
}

// GlobalkeyBindings is a global, immutable map of KeyName tot keybinding.
var GlobalkeyBindings = map[KeyName]key.Binding{
	KeyUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	KeyDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	KeyShiftUp: key.NewBinding(
		key.WithKeys("shift+up"),
		key.WithHelp("shift+↑", "scroll"),
	),
	KeyShiftDown: key.NewBinding(
		key.WithKeys("shift+down"),
		key.WithHelp("shift+↓", "scroll"),
	),
	KeyEnter: key.NewBinding(
		key.WithKeys("enter", "o"),
		key.WithHelp("↵/o", "open"),
	),
	KeyNew: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	KeyKill: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "kill"),
	),
	KeyHelp: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	KeyQuit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	KeySubmit: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "push"),
	),
	KeyPrompt: key.NewBinding(
		key.WithKeys("N"),
		key.WithHelp("N", "new with prompt"),
	),
	KeyCheckout: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "checkout"),
	),
	KeyTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch"),
	),
	KeyResume: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "resume"),
	),
	KeyRestore: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "restore killed"),
	),
	KeyNextAttention: key.NewBinding(
		key.WithKeys("]"),
		key.WithHelp("]", "next waiting"),
	),
	KeyPageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("pgup", "page up"),
	),
	KeyPageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("pgdn", "page down"),
	),
	KeySendPrompt: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "send prompt"),
	),
	KeyOpenPR: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "open PR"),
	),
	KeyUpdate: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "update"),
	),
	KeySettings: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "settings"),
	),
	KeyTheme: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "theme"),
	),
	KeyDev: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "run"),
	),
	KeyDevStop: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "stop"),
	),

	KeyMoveUp: key.NewBinding(
		key.WithKeys("K"),
		key.WithHelp("K", "move up"),
	),
	KeyMoveDown: key.NewBinding(
		key.WithKeys("J"),
		key.WithHelp("J", "move down"),
	),

	// -- Special keybindings --

	KeySubmitName: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "submit name"),
	),

	KeyPickBranch: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "pick branch"),
	),
	KeyNextField: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	KeyConfirm: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "submit"),
	),
	KeyCancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
}
