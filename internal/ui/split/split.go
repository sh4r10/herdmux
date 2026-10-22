// Package split is the split-popup layout shared by the agent picker, the
// window organiser and the colorscheme picker: a framed popup with a header
// line, a list on the left, a panel on the right (a live pane preview, or
// swatches), and one footer line of hints.
//
//	╭─ title ─────────────────────────────────────────╮
//	│ header                                          │
//	│                                                 │
//	│ list rows…            │ right panel…            │
//	│ (Above: e.g. a red config error)                │
//	│ footer                                          │
//	╰─────────────────────────────────────────────────╯
//
// It also holds the pieces around it: the 250 ms refresh Tick, the preview
// Capture, list scrolling (Offset) and the `/` filter (Filter, Match).
package split

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/ui/style"
)

// Layout constants.
const (
	Gutter  = 1  // blank cells inside the frame on each side
	MaxList = 58 // the list column's widest
	// Chrome is the lines a layout uses besides the body: the frame's top
	// and bottom, the header, the blank line under it and the footer.
	Chrome = 5
)

// Inner is the content width inside a w-wide frame (frame and gutters off).
func Inner(w int) int { return max(0, w-2-2*Gutter) }

// Columns splits an inner width into the list's and the right panel's
// widths: the list takes 45% (at most MaxList), then " │ ", then the rest.
func Columns(inner int) (left, right int) {
	left = min(MaxList, inner*45/100)
	return left, max(0, inner-left-3)
}

// BodyHeight is the number of list/panel rows in an h-high popup with extra
// lines between the body and the footer (Layout.Above).
func BodyHeight(h, extra int) int { return max(1, h-Chrome-extra) }

// Layout is one frame of a split popup.
type Layout struct {
	Title  string   // on the frame's top edge, in accent
	Header string   // first line: scope, order, filter
	Left   []string // list rows, each cut/padded to the left column
	Right  []string // right panel lines, cut/padded to the right column
	Above  []string // full-width lines above the footer (errors, prompts)
	Footer string   // key hints or a message
}

// Render draws the layout w×h cells, frame included. Left and Right are cut
// or padded to the columns (Columns(Inner(w))) and to BodyHeight rows.
func (l Layout) Render(st *style.Styles, w, h int) string {
	inner := Inner(w)
	lw, rw := Columns(inner)
	bh := BodyHeight(h, len(l.Above))
	gut := st.Style().Render(strings.Repeat(" ", Gutter)) // painted bg when st.Opaque
	sep := st.Fg("border").Render("│")
	lines := make([]string, 0, h-2)
	add := func(s string) { lines = append(lines, gut+st.Pad(s, inner, "")) }
	add(l.Header)
	add("")
	for i := range bh {
		var left, right string
		if i < len(l.Left) {
			left = l.Left[i]
		}
		if i < len(l.Right) {
			right = l.Right[i]
		}
		add(st.Pad(left, lw, "") + st.Style().Render(" ") + sep + st.Style().Render(" ") + st.Pad(right, rw, ""))
	}
	for _, a := range l.Above {
		add(a)
	}
	add(l.Footer)
	return st.Frame(l.Title, w, h, lines)
}

// Offset is the first list row to draw so the cursor stays visible in h rows.
func Offset(cur, h int) int { return max(0, cur-h+1) }

// RefreshInterval is how often a split popup refreshes its preview (and
// list); ~250 ms is live enough.
const RefreshInterval = 250 * time.Millisecond

// Tick is the refresh message; Every schedules the next one. Return Every()
// from Init and again from Update on every Tick.
type Tick struct{}

// Every schedules a Tick after RefreshInterval.
func Every() tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg { return Tick{} })
}

// Preview fits captured pane lines into a w×h panel: the last h lines, each
// cut to w cells and ended with an SGR reset, so a pane's colours never leak
// into the padding or the next line.
func Preview(lines []string, w, h int) []string {
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, w, "") + "\x1b[0m"
	}
	return out
}
