package overlay

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/AlexanderWeismannn/adroit/theme"
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
	// layout, when set, lays the card out for the width it is drawn at (the
	// frame's inside less its air), so a card can wrap its lines and choose its
	// columns rather than be cut at the border.
	layout func(width int) string

	width, height int
	// MaxWidth, when set, caps the width SetSize and SetWidth are given.
	MaxWidth int
	// offset is the first body line shown while the card is taller than the
	// screen and scrolls.
	offset int
}

// NewTextOverlay creates a new text screen overlay with the given title and content
func NewTextOverlay(content string) *TextOverlay {
	return &TextOverlay{
		Dismissed: false,
		content:   content,
	}
}

// NewTextOverlayLayout is a text overlay whose card is laid out for the width
// it is drawn at.
func NewTextOverlayLayout(layout func(width int) string) *TextOverlay {
	return &TextOverlay{layout: layout}
}

// HandleKeyPress processes a key press and updates the state.
//
// Returns whether the overlay should be closed, and whatever the dismissal
// callback wants run. A card taller than the screen scrolls with the arrows,
// j/k, page up/down and home/end; every other key closes it, as before.
func (t *TextOverlay) HandleKeyPress(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if lines, rows := t.body(); len(lines) > rows {
		page := max(rows-2, 1)
		switch msg.String() {
		case "up", "k":
			t.scroll(-1, len(lines), rows)
			return false, nil
		case "down", "j":
			t.scroll(1, len(lines), rows)
			return false, nil
		case "pgup", "shift+up":
			t.scroll(-page, len(lines), rows)
			return false, nil
		case "pgdown", "shift+down", "space":
			t.scroll(page, len(lines), rows)
			return false, nil
		case "home", "g":
			t.offset = 0
			return false, nil
		case "end", "G":
			t.scroll(len(lines), len(lines), rows)
			return false, nil
		}
	}
	t.Dismissed = true
	// Call the OnDismiss callback if it exists
	var cmd tea.Cmd
	if t.OnDismiss != nil {
		cmd = t.OnDismiss()
	}
	return true, cmd
}

func (t *TextOverlay) scroll(by, total, rows int) {
	t.offset = min(max(t.offset+by, 0), max(total-rows, 0))
}

// outerWidth is the frame's width: as set, or as wide as the card when unset.
func (t *TextOverlay) outerWidth() int {
	if t.width > 0 {
		return t.width
	}
	if t.layout != nil {
		return lipgloss.Width(t.layout(80)) + 6
	}
	return lipgloss.Width(t.content) + 6
}

// body is the card's lines at the current width, and how many of them fit
// (all of them when no height is set).
func (t *TextOverlay) body() (lines []string, rows int) {
	width := t.outerWidth()
	content := t.content
	if t.layout != nil {
		// Two cells of air each side inside the border.
		content = t.layout(max(width-6, 1))
	}
	lines = strings.Split(strings.TrimRight(content, "\n"), "\n")
	if t.height <= 0 {
		return lines, len(lines)
	}
	// Measured with the widest position it could show, so a wrap of the keys
	// line is paid for before it happens.
	return lines, max(t.height-FrameChrome(width, t.footer(true, 999, 999, 999)), 1)
}

// footer is the keys line: "any key close" for a card that fits, the scroll
// keys and where you are in it for one that does not.
func (t *TextOverlay) footer(scrolls bool, first, last, total int) string {
	if !scrolls {
		return Keys(Key{"any key", "close"})
	}
	where := lipgloss.NewStyle().Foreground(theme.Color(theme.Muted)).
		Render(fmt.Sprintf("%d–%d of %d", first, last, total))
	return Keys(Key{"↑↓", "scroll"}, Key{"pgup/pgdn", "page"}, Key{"esc", "close"}) + "   " + where
}

// Render renders the text overlay
func (t *TextOverlay) Render(opts ...WhitespaceOption) string {
	lines, rows := t.body()
	if len(lines) <= rows {
		t.offset = 0
		return Frame(t.outerWidth(), t.Title, strings.Join(lines, "\n"), t.footer(false, 0, 0, 0), theme.Color(theme.Accent))
	}
	t.scroll(0, len(lines), rows) // a resize can leave the offset past the end
	shown := lines[t.offset : t.offset+rows]
	footer := t.footer(true, t.offset+1, t.offset+rows, len(lines))
	return Frame(t.outerWidth(), t.Title, strings.Join(shown, "\n"), footer, theme.Color(theme.Accent))
}

// SetWidth sets the frame's outer width and leaves its height unbounded.
func (t *TextOverlay) SetWidth(width int) {
	t.width = t.capWidth(width)
}

func (t *TextOverlay) capWidth(width int) int {
	if t.MaxWidth > 0 && width > t.MaxWidth {
		return t.MaxWidth
	}
	return width
}

// SetSize sets the frame's outer width and the most rows it may take; a card
// taller than that scrolls inside a fixed frame, keys line included.
func (t *TextOverlay) SetSize(width, height int) {
	t.width, t.height = t.capWidth(width), height
}
