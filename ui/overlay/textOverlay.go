package overlay

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TextOverlay represents a text screen overlay
type TextOverlay struct {
	// Whether the overlay has been dismissed
	Dismissed bool
	// OnDismiss is called when the overlay is dismissed, and may return work for
	// the caller to hand back to bubbletea.
	//
	// It returns a Cmd rather than doing the work itself because some of what a
	// help screen gates -- attaching to a session -- must not run inside Update.
	// See home.attachCmd.
	OnDismiss func() tea.Cmd
	// Title goes in the border; content is the card.
	Title   string
	content string

	width int
}

// NewTextOverlay creates a new text screen overlay with the given title and content
func NewTextOverlay(content string) *TextOverlay {
	return &TextOverlay{
		Dismissed: false,
		content:   content,
	}
}

// HandleKeyPress processes a key press and updates the state.
//
// Returns whether the overlay should be closed, and whatever the dismissal
// callback wants run.
func (t *TextOverlay) HandleKeyPress(msg tea.KeyMsg) (bool, tea.Cmd) {
	// Close on any key
	t.Dismissed = true
	// Call the OnDismiss callback if it exists
	var cmd tea.Cmd
	if t.OnDismiss != nil {
		cmd = t.OnDismiss()
	}
	return true, cmd
}

// Render renders the text overlay
func (t *TextOverlay) Render(opts ...WhitespaceOption) string {
	width := t.width
	if width <= 0 {
		// Unsized: as wide as the card, like the border used to be.
		width = lipgloss.Width(t.content) + 6
	}
	return Frame(width, t.Title, t.content, Keys(Key{"any key", "close"}), theme.Color(theme.Accent))
}

func (t *TextOverlay) SetWidth(width int) {
	t.width = width
}
