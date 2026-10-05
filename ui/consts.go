package ui

import "github.com/charmbracelet/lipgloss"

// BlankCell is U+2800 BRAILLE PATTERN BLANK: a cell that takes up width but is
// not a space, and so survives the whitespace trim the preview pane applies
// before it centres. Pad with it any block whose lines must stay aligned with
// one another once centred -- see FallBackText and newInstanceHint.
const BlankCell = "⠀"

// FallBackText is the splash shown in a pane when there is no session output to
// show. The Adroit symbol -- the threaded lowercase a -- rendered from
// assets/adroit-symbol.svg.
//
// Braille rather than an inline image: a braille cell is 2x4 dots and a terminal
// cell is about twice as tall as it is wide, which makes each dot very nearly
// square, so the mark keeps its proportions. It is also just text, so it inherits
// the pane's colour and every theme recolours it for free -- where a sixel or
// kitty-protocol image would be a fixed bitmap the redraw would smear, since
// bubbletea repaints by rewriting lines.
//
// Every line is padded to the same width with U+2800 BRAILLE PATTERN BLANK, not
// spaces: lipgloss centres a block by its widest line, and ragged lines would
// each centre differently and tilt the mark.
var FallBackText = "\n" + `⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣤⣤⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⢀⣤⠶⠛⠋⠉⠛⠛⢶⣄⠀⣿⠀⠀⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⢀⡴⠋⠀⠀⠀⠀⠀⠀⠀⠀⠙⣧⠘⠓⠚⠁⠀⠀⠀⣀⣤⠶⠶⠶⠀
⠀⠀⠙⠀⠀⠀⠀⠀⠀⢀⣀⣀⣠⣤⣼⣦⡤⠤⠶⠶⠚⠋⠉⠀⠀⠀⠀⠀
⠀⠀⠀⠀⣀⣤⠴⠖⠛⠋⠉⠉⠀⠀⢸⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⣠⠞⠋⠀⠀⠀⠀⠀⠀⠀⢀⣀⣼⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⣼⠃⠀⠀⠀⠀⠀⠀⢀⣴⠞⠉⣹⢿⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⣿⠀⠀⠀⠀⠀⣀⡴⠋⠀⢀⣴⠋⠀⢻⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠘⠷⣤⣤⡴⠞⠋⠀⢀⣤⠞⠁⠀⠀⠀⠙⢦⣄⣤⡄⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠉⠛⠛⠛⠛⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`

// LogoMarkSmall is the symbol at the smallest size it still reads at, for a
// header beside a title where the full splash would not fit. Regenerate with
// `python3 scripts/render-brand.py mark-small`.
//
// Padded with U+2800 like FallBackText, and for the same reason: it is joined
// horizontally against a block of text, and a ragged edge would leave the two
// columns crooked relative to each other.
var LogoMarkSmall = `⠀⠀⠀⠀⢀⣀⣀⣀⣀⠀⢀⡤⠦⡄⠀⠀⠀⠀⠀⠀
⠀⠀⣠⠞⠉⠀⠀⠀⠈⠳⡌⠧⠴⠃⠀⠀⣀⣠⣤⠄
⠀⠘⠁⠀⠀⢀⣀⣀⡤⠤⢽⠤⠴⠖⠒⠋⠁⠀⠀⠀
⠀⠀⡠⠖⠋⠉⠀⠀⠀⠀⣸⠀⠀⠀⠀⠀⠀⠀⠀⠀
⢀⡞⠀⠀⠀⠀⢀⡤⠚⢹⢧⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠈⣇⠀⠀⣀⠴⠋⢀⡴⠃⠀⢳⡀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠙⠛⠳⠶⠒⠋⠀⠀⠀⠀⠉⠉⠁⠀⠀⠀⠀⠀`

// splash lays the mark above a message, centred, for the panes that have nothing
// to show yet.
//
// The mark is dropped in a pane too narrow to draw it: it is 28 cells wide and
// nothing wraps a picture usefully -- below that width every second row came out
// as the overflow of the row above it, which reads as a rendering fault rather
// than as a logo, and it pushed the message that actually says something off the
// bottom of the pane.
func splash(width int, message string) string {
	if width < lipgloss.Width(FallBackText) {
		return message
	}
	return lipgloss.JoinVertical(lipgloss.Center, FallBackText, "", message)
}
