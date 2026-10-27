package overlay

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ConfirmationOverlay represents a confirmation dialog overlay
type ConfirmationOverlay struct {
	// Whether the overlay has been dismissed
	Dismissed bool
	// Title is what the dialog is about, in the border; message is the body:
	// what is lost, and what can be recovered.
	Title string
	// ConfirmLabel and CancelLabel are the footer's words for the two keys.
	ConfirmLabel, CancelLabel string
	// Message to display in the overlay
	message string
	// Width of the overlay
	width int
	// Callback function to be called when the user confirms (presses 'y')
	OnConfirm func()
	// Callback function to be called when the user cancels (presses 'n' or 'esc')
	OnCancel func()
	// Custom confirm key (defaults to 'y')
	ConfirmKey string
	// Custom cancel key (defaults to 'n')
	CancelKey string
	// Custom styling options
	borderColor lipgloss.TerminalColor
}

// NewConfirmationOverlay creates a new confirmation dialog overlay with the given message
func NewConfirmationOverlay(message string) *ConfirmationOverlay {
	return &ConfirmationOverlay{
		Dismissed:    false,
		message:      message,
		width:        50, // Default width
		ConfirmKey:   "y",
		CancelKey:    "n",
		ConfirmLabel: "confirm",
		CancelLabel:  "cancel",
		// Danger, because every confirmation is guarding something destructive.
		borderColor: theme.Color(theme.Danger),
	}
}

// HandleKeyPress processes a key press and updates the state
// Returns true if the overlay should be closed
func (c *ConfirmationOverlay) HandleKeyPress(msg tea.KeyMsg) bool {
	switch msg.String() {
	case c.ConfirmKey:
		c.Dismissed = true
		if c.OnConfirm != nil {
			c.OnConfirm()
		}
		return true
	case c.CancelKey, "esc":
		c.Dismissed = true
		if c.OnCancel != nil {
			c.OnCancel()
		}
		return true
	default:
		// Ignore other keys in confirmation state
		return false
	}
}

// Render renders the confirmation overlay
func (c *ConfirmationOverlay) Render(opts ...WhitespaceOption) string {
	// Wrapped to the inside of the frame; a long line of consequences is
	// otherwise cut.
	body := lipgloss.NewStyle().Width(max(c.width-6, 1)).Render(c.message)
	footer := Keys(Key{c.ConfirmKey, c.ConfirmLabel}, Key{c.CancelKey + "/esc", c.CancelLabel})
	return Frame(c.width, c.Title, body, footer, c.borderColor)
}

// SetWidth sets the width of the confirmation overlay
func (c *ConfirmationOverlay) SetWidth(width int) {
	c.width = width
}

// SetBorderColor sets the border color of the confirmation overlay
func (c *ConfirmationOverlay) SetBorderColor(color lipgloss.TerminalColor) {
	c.borderColor = color
}

// SetConfirmKey sets the key used to confirm the action
func (c *ConfirmationOverlay) SetConfirmKey(key string) {
	c.ConfirmKey = key
}

// SetCancelKey sets the key used to cancel the action
func (c *ConfirmationOverlay) SetCancelKey(key string) {
	c.CancelKey = key
}
