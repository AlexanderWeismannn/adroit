package ui

import (
	"fmt"
	"strings"

	"github.com/AlexanderWeismannn/adroit/theme"
	"github.com/charmbracelet/lipgloss"
)

// Header is the strip along the top of the screen: where you are, how many
// sessions there are and how many need you, and the mode the screen is in.
//
//	Adroit   ~/postilize · master   4 sessions · 1 needs you        split · 110×30
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

var headerNameStyle, headerMutedStyle, headerTextStyle lipgloss.Style

func init() { theme.OnChange(applyHeaderTheme) }

func applyHeaderTheme() {
	headerNameStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Color(theme.Accent))
	headerMutedStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	headerTextStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
}

func NewHeader() *Header { return &Header{} }

func (h *Header) SetWidth(width int) { h.width = width }

func (h *Header) String() string {
	if h.width <= 0 {
		return ""
	}
	left := " " + headerNameStyle.Render("Adroit")
	if h.Repo != "" {
		where := h.Repo
		if h.Base != "" {
			where += " · " + h.Base
		}
		left += "   " + headerMutedStyle.Render(where)
	}
	count := fmt.Sprintf("%d sessions", h.Sessions)
	if h.Sessions == 1 {
		count = "1 session"
	}
	switch {
	case h.NeedYou == 1:
		count += " · 1 needs you"
	case h.NeedYou > 1:
		count += fmt.Sprintf(" · %d need you", h.NeedYou)
	}
	left += "   " + headerTextStyle.Render(count)

	right := headerMutedStyle.Render(fmt.Sprintf("%s · %d×%d", h.Mode, h.Cols, h.Rows)) + " "

	gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Narrow: the size goes first, then the place; the name and the counts
		// are what is left.
		right = ""
		gap = h.width - lipgloss.Width(left)
	}
	if gap < 0 {
		return clip(left, h.width)
	}
	return left + strings.Repeat(" ", gap) + right
}
