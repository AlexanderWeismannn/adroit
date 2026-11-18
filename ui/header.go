package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/AlexanderWeismannn/adroit/theme"
)

// Header is the strip along the top of the screen: where you are, how many
// sessions there are and how many need you, and the mode the screen is in.
//
//	Adroit   ~/postilize · master   4 sessions · 1 needs you                split
//
// It replaces the blank row each pane used to lead with, so it costs nothing.
type Header struct {
	width int
	// Repo is the repository new sessions are created in, shown as typed at a
	// shell (~ for the home directory); Base is its current branch.
	Repo, Base string
	// Mode is the layout mode's name; Cols and Rows the terminal's size.
	Mode       string
	Cols, Rows int
	// Sessions is how many there are; NeedYou how many are waiting on an answer
	// or have a finish not yet looked at.
	Sessions, NeedYou int
}

var headerNameStyle, headerMutedStyle, headerTextStyle, headerAttentionStyle lipgloss.Style

func init() { theme.OnChange(applyHeaderTheme) }

func applyHeaderTheme() {
	headerNameStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent))
	headerMutedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	headerTextStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	// The count of sessions that need you is the one thing on the strip that
	// asks for something, so it wears the colour of the waiting mark.
	headerAttentionStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Warning))
}

// Diagnostics is whether the interface shows the numbers that are only there
// to measure it -- the terminal's size in the header, the pane's sample rate in
// the window footer and the status column. On only with ADROIT_PROFILE, which
// is when someone is looking at them.
var Diagnostics = os.Getenv("ADROIT_PROFILE") != ""

func NewHeader() *Header { return &Header{} }

func (h *Header) SetWidth(width int) { h.width = width }

// String draws the strip. When it does not fit, what gives way is what you
// already know: the terminal size first, then the repository and its branch
// (shortened, then dropped), then the mode. The session count and how many
// need you are the last to go -- they were the first, cut from the right.
func (h *Header) String() string {
	if h.width <= 0 {
		return ""
	}
	name := " " + headerNameStyle.Render("Adroit")
	count := fmt.Sprintf("%d sessions", h.Sessions)
	if h.Sessions == 1 {
		count = "1 session"
	}
	counts := headerTextStyle.Render(count)
	switch {
	case h.NeedYou == 1:
		counts += headerMutedStyle.Render(" · ") + headerAttentionStyle.Render("1 needs you")
	case h.NeedYou > 1:
		counts += headerMutedStyle.Render(" · ") + headerAttentionStyle.Render(fmt.Sprintf("%d need you", h.NeedYou))
	}
	where := h.Repo
	if where != "" && h.Base != "" {
		where += " · " + h.Base
	}
	mode := h.Mode
	if Diagnostics && h.Cols > 0 {
		mode += fmt.Sprintf(" · %d×%d", h.Cols, h.Rows)
	}

	compose := func(where, mode string) string {
		left := name
		if where != "" {
			left += "   " + headerMutedStyle.Render(where)
		}
		left += "   " + counts
		if mode == "" {
			return left
		}
		right := headerMutedStyle.Render(mode) + " "
		gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			return ""
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if line := compose(where, mode); line != "" {
		return line
	}
	if line := compose(where, h.Mode); line != "" {
		return line
	}
	// The place is shortened from its middle, so both the repository and the
	// branch keep the ends that tell them apart, and dropped once it is too short
	// to say anything.
	if where != "" {
		// The name, the counts and the mode with their gaps, and the trailing cell.
		fixed := lipgloss.Width(name) + 3 + lipgloss.Width(counts) + 3 + lipgloss.Width(h.Mode) + 1
		if fixed < h.width {
			if room := h.width - fixed - 3; room >= 12 {
				if line := compose(elideMiddle(where, room), h.Mode); line != "" {
					return line
				}
			}
			return compose("", h.Mode)
		}
	}
	return clip(compose("", ""), h.width)
}

// ShortenHome writes a path the way it is typed at a shell: ~ for the home
// directory.
func ShortenHome(path string) string {
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(filepath.Separator)) {
			return "~" + path[len(home):]
		}
	}
	return path
}
