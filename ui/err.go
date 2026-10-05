package ui

import (
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ErrBox is the one-row footer that reports what just happened.
//
// It carries either an error or a notice, never both, because it has one row
// and the most recent thing is the one worth reading. A notice exists because
// not everything worth telling the user is a failure: resuming a session that
// came back with its conversation intact looks exactly like resuming one that
// came back empty, and the difference is the whole point.
type ErrBox struct {
	height, width int
	err           error
	notice        string
}

// errIcon names the line as an error without relying on colour alone -- the box
// is one row in a busy footer and a bare red string is easy to miss.
const errIcon = "✗ "

// noticeIcon does the same for the other kind, and must not be mistakable for
// it at a glance.
const noticeIcon = "✓ "

// Pure #FF0000 is harsher than anything else on screen and reads as a crash
// rather than as "that did not work". theme.Danger is the red the diff stats and
// the failing-build glyph already use.
var errStyle, noticeStyle lipgloss.Style

func init() { theme.OnChange(applyErrTheme) }

func applyErrTheme() {
	errStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
	noticeStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
}

func NewErrBox() *ErrBox {
	return &ErrBox{}
}

func (e *ErrBox) SetError(err error) {
	e.err = err
	e.notice = ""
}

// SetNotice reports something that went right and is not obvious from the
// screen.
func (e *ErrBox) SetNotice(notice string) {
	e.notice = notice
	e.err = nil
}

func (e *ErrBox) Clear() {
	e.err = nil
	e.notice = ""
}

func (e *ErrBox) SetSize(width, height int) {
	e.width = width
	e.height = height
}

func (e *ErrBox) String() string {
	text, style := "", errStyle
	switch {
	case e.err != nil:
		text = errIcon + collapse(e.err.Error())
	case e.notice != "":
		text, style = noticeIcon+collapse(e.notice), noticeStyle
	}

	if runewidth.StringWidth(text) > e.width-3 && e.width-3 >= 0 {
		text = runewidth.Truncate(text, e.width-3, "…")
	}
	return lipgloss.Place(e.width, e.height, lipgloss.Center, lipgloss.Center, style.Render(text))
}

// collapse folds a multi-line message onto the box's single row. " · " reads as
// a separator where "//" read as part of a path.
func collapse(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " · ")
}
