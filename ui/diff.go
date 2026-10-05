package ui

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/theme"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var (
	AdditionStyle lipgloss.Style
	DeletionStyle lipgloss.Style
	HunkStyle     lipgloss.Style
	DiffFileStyle lipgloss.Style
	DiffMetaStyle lipgloss.Style
)

type DiffPane struct {
	viewport viewport.Model
	diff     string
	stats    string
	width    int
	height   int

	// srcInstance and srcAt identify the diff the pane has already colourised.
	//
	// SetDiff runs on every preview tick, and colourising is a per-line restyle
	// of the whole diff plus a join of the result: measured at 2.4ms and 1.7MB
	// allocated for a 200KB diff, which at ten ticks a second was 17MB/s of
	// garbage to redraw something that had not changed. The instance stamps its
	// stats when it recomputes them, so the pair is exact -- no hashing, and no
	// chance of missing a change that kept the same size.
	srcInstance *session.Instance
	srcAt       time.Time
	srcTheme    int
}

func NewDiffPane() *DiffPane {
	return &DiffPane{
		viewport: viewport.New(0, 0),
	}
}

func (d *DiffPane) SetSize(width, height int) {
	width, height = max(width, 0), max(height, 0)
	d.width = width
	d.height = height
	d.viewport.Width = width
	d.viewport.Height = height
	// Update viewport content if diff exists
	if d.diff != "" || d.stats != "" {
		d.viewport.SetContent(d.content())
	}
}

func (d *DiffPane) SetDiff(instance *session.Instance) {
	// Built where it is used, not up front: this is a full-pane string, and
	// SetDiff runs on every preview tick, where the ordinary case returns without
	// needing it at all.
	centeredFallbackMessage := func() string {
		return d.centered("No changes")
	}

	if instance == nil || !instance.Started() {
		d.forget()
		d.viewport.SetContent(centeredFallbackMessage())
		return
	}

	stats := instance.GetDiffStats()
	if stats == nil {
		// Show loading message if worktree is not ready
		d.forget()
		d.viewport.SetContent(d.centered("Setting up worktree..."))
		return
	}

	if stats.Error != nil {
		d.forget()
		d.viewport.SetContent(d.centered(fmt.Sprintf("Error: %v", stats.Error)))
		return
	}

	if stats.IsEmpty() {
		d.stats = ""
		d.diff = ""
		d.srcInstance, d.srcAt = nil, time.Time{}
		d.viewport.SetContent(centeredFallbackMessage())
	} else {
		// Already rendered, and the stats have not been recomputed since.
		if d.srcInstance == instance && d.srcAt.Equal(instance.DiffStatsAt()) && d.srcTheme == diffThemeGeneration &&
			d.diff != "" {
			return
		}
		d.srcInstance, d.srcAt, d.srcTheme = instance, instance.DiffStatsAt(), diffThemeGeneration
		var files int
		d.diff, files = colorizeDiff(stats.Content)
		noun := "files"
		if files == 1 {
			noun = "file"
		}
		d.stats = fmt.Sprintf("%d %s · ", files, noun) +
			AdditionStyle.Render(fmt.Sprintf("+%d", stats.Added)) + " " +
			DeletionStyle.Render(fmt.Sprintf("−%d", stats.Removed)) + "\n"
		d.viewport.SetContent(d.content())
	}
}

// content is what the viewport shows: the summary line over the diff, cut to
// the pane's width.
func (d *DiffPane) content() string {
	return clipLines(lipgloss.JoinVertical(lipgloss.Left, d.stats, d.diff), d.width)
}

// centered fills the pane with one message.
func (d *DiffPane) centered(message string) string {
	return lipgloss.Place(d.width, d.height, lipgloss.Center, lipgloss.Center, message)
}

// forget drops the colourised diff and what identified it, so the next real diff
// is rebuilt rather than compared against a stale stamp.
func (d *DiffPane) forget() {
	d.diff, d.stats = "", ""
	d.srcInstance, d.srcAt = nil, time.Time{}
}

func (d *DiffPane) String() string {
	return d.viewport.View()
}

// IsScrolling reports whether the diff is scrolled away from its top. The diff
// has no live mode to pause, so this is only a position.
func (d *DiffPane) IsScrolling() bool {
	return d.viewport.YOffset > 0
}

// ScrollUp scrolls the viewport up
func (d *DiffPane) ScrollUp() {
	d.viewport.LineUp(1)
}

// ScrollDown scrolls the viewport down
func (d *DiffPane) ScrollDown() {
	d.viewport.LineDown(1)
}

// colorizeDiff styles a unified diff for the pane, and returns it with the
// number of files it touches.
//
// Each file opens with a bar naming it and its own counts, in place of the four
// lines of git plumbing -- "diff --git", "index", "---", "+++" -- that used to
// render as plain context text, indistinguishable from the code around them.
func colorizeDiff(diff string) (string, int) {
	lines := strings.Split(diff, "\n")

	// A first pass for the per-file counts the header bar carries.
	type fileStat struct{ added, removed int }
	var stats []fileStat
	header := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			stats = append(stats, fileStat{})
			header = true
		case len(stats) == 0:
		case strings.HasPrefix(line, "@@"):
			header = false
		case header:
			// "--- a/x" and "+++ b/x" are names, not lines; a removed line that
			// happens to start with "--" is past the hunk header and counted.
		case strings.HasPrefix(line, "+"):
			stats[len(stats)-1].added++
		case strings.HasPrefix(line, "-"):
			stats[len(stats)-1].removed++
		}
	}

	var out strings.Builder
	file := -1
	inHeader := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file++
			inHeader = true
			if file > 0 {
				out.WriteString("\n")
			}
			out.WriteString(diffFileHeader(diffPath(line), stats[file].added, stats[file].removed) + "\n")
		case inHeader && (strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ")):
			// Plumbing the bar above already says.
		case inHeader && !strings.HasPrefix(line, "@@"):
			// "new file mode", "deleted file mode", "rename from": worth
			// keeping, but not as loud as the code.
			out.WriteString(DiffMetaStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "@@"):
			inHeader = false
			out.WriteString(HunkStyle.Render(line) + "\n")
		case len(line) > 0 && line[0] == '+':
			out.WriteString(AdditionStyle.Render(line) + "\n")
		case len(line) > 0 && line[0] == '-':
			out.WriteString(DeletionStyle.Render(line) + "\n")
		default:
			out.WriteString(line + "\n")
		}
	}
	return out.String(), len(stats)
}

// diffPath is the file a "diff --git a/X b/Y" line is about: the new name.
func diffPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return rest
}

func diffFileHeader(path string, added, removed int) string {
	counts := ""
	if added > 0 {
		counts += " " + AdditionStyle.Render("+"+compactCount(added))
	}
	if removed > 0 {
		counts += " " + DeletionStyle.Render("−"+compactCount(removed))
	}
	return DiffFileStyle.Render("▍ "+path) + counts
}

// clipLines cuts every line of the diff to the pane. The viewport wraps anything
// wider, and a wrapped continuation row has no +/- in front of it, so it read as
// context -- and the extra rows pushed the bottom of the diff out of the pane.
func clipLines(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = clip(line, width)
	}
	return strings.Join(lines, "\n")
}

func init() { theme.OnChange(applyDiffTheme) }

func applyDiffTheme() {
	AdditionStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Success))
	DeletionStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Danger))
	HunkStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Info))
	DiffFileStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Accent)).Bold(true)
	DiffMetaStyle = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	// A diff already on screen was colourised with the old palette, and the
	// pane's cache would keep serving it: the theme picker previewed every
	// colour but the diff's.
	diffThemeGeneration++
}

// diffThemeGeneration counts palette changes, so the pane knows its cached
// colourising is stale.
var diffThemeGeneration int
