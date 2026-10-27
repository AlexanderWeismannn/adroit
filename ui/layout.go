package ui

// Mode is how the screen is divided. It is picked from the terminal's width,
// and a key can override the pick.
type Mode int

const (
	// Compact shows one pane at a time -- the list, or the window with a
	// one-line strip naming the session above it -- and tab swaps them.
	Compact Mode = iota
	// Split shows the list beside the window.
	Split
	// Wide is Split with a status column for the selected session on the
	// right, so the tab bar and the rows no longer have to carry status.
	Wide
)

func (m Mode) String() string {
	switch m {
	case Compact:
		return "compact"
	case Split:
		return "split"
	case Wide:
		return "wide"
	}
	return "?"
}

// The breakpoints, in columns. Below CompactBelow the list and the window
// cannot both be read; from WideFrom there is room for a third column.
const (
	CompactBelow = 90
	WideFrom     = 160
)

// Pick is the mode the width calls for.
func Pick(width int) Mode {
	switch {
	case width < CompactBelow:
		return Compact
	case width >= WideFrom:
		return Wide
	}
	return Split
}

// A Rect is a component's place on the screen. A component draws itself
// exactly W wide and H tall, or smaller, and never wider: the frame is composed
// from these and nothing cuts it to size afterwards.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether the rect has no cells.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Rects is where everything goes for one terminal size and mode. A rect a mode
// does not use is empty.
type Rects struct {
	Mode Mode
	// Width and Height are the terminal's.
	Width, Height int
	// Header is the strip along the top: repo, base, session counts, mode.
	Header Rect
	// List is the session list; Window the tabbed panes. In Compact they share
	// the same rows and only one is shown.
	List, Window Rect
	// Strip is the one-line session name above the window in Compact.
	Strip Rect
	// Side is the status column in Wide.
	Side Rect
	// Bar is the context bar; Notice the line under it for notices and errors.
	Bar, Notice Rect
}

const (
	headerRows = 1
	barRows    = 1
	noticeRows = 1
	// sideWidth is the status column's width in Wide, and the list's beside it.
	sideWidth = 36
)

// Layout lays the components out for a terminal of width x height in mode.
//
// Pure, so it can be tabled: the rows add up to the height exactly (a frame one
// row taller than the terminal scrolls the whole view on every redraw) and the
// columns to the width, and nothing is ever negative.
func Layout(width, height int, mode Mode) Rects {
	r := Rects{Mode: mode, Width: width, Height: height}
	width, height = max(width, 0), max(height, 0)

	r.Header = Rect{0, 0, width, min(headerRows, height)}
	top := r.Header.H
	// The bar and the notice line take the last rows; everything else is content.
	content := max(height-top-barRows-noticeRows, 0)
	r.Bar = Rect{0, top + content, width, min(barRows, max(height-top-content, 0))}
	r.Notice = Rect{0, r.Bar.Y + r.Bar.H, int(float32(width) * 0.9), min(noticeRows, max(height-r.Bar.Y-r.Bar.H, 0))}

	switch mode {
	case Compact:
		r.List = Rect{0, top, width, content}
		r.Strip = Rect{0, top, width, min(1, content)}
		r.Window = Rect{0, top + r.Strip.H, width, max(content-r.Strip.H, 0)}
	case Wide:
		list := min(sideWidth, width)
		side := min(sideWidth, max(width-list, 0))
		r.List = Rect{0, top, list, content}
		r.Side = Rect{width - side, top, side, content}
		r.Window = Rect{list, top, max(width-list-side, 0), content}
	default:
		// List takes 30% of width, preview takes 70%
		list := int(float32(width) * 0.3)
		r.List = Rect{0, top, list, content}
		r.Window = Rect{list, top, width - list, content}
	}
	return r
}
