// Package sessionizer is the session switcher / project launcher popup: a
// split layout with the fuzzy-filtered session/dir list on the left and,
// on the right, the selected entry's full path plus a live preview of a
// session's active pane or a listing of a directory.
//
// The popup opens typing into the filter (fzf-like): printable keys
// filter, Ctrl-N/Ctrl-P and the arrows move, ⏎ or Ctrl-Y switches/launches,
// Esc clears the filter and then closes.
package sessionizer

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/sessionizer"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// Backend is what the model needs from tmux.
type Backend interface {
	Entries() ([]sessionizer.Entry, error) // list sessions and dirs
	Capture(pane string, n int) []string   // the last n lines of pane, for the preview
}

// The switch itself happens after the popup closes: the verb passes Result to
// sessionizer.SwitchTo with the client name.

// Options configure New.
type Options struct {
	Backend   Backend
	Styles    *style.Styles
	Now       func() time.Time // nil: time.Now
	ConfigErr error            // the config.toml error, if any (shown in red)
}

// Result is what the user chose.
type Result struct {
	Switch *sessionizer.Entry // ⏎: switch or launch this entry
}

// row is a listed entry with its fuzzy match.
type row struct {
	sessionizer.Entry
	pos   []int // matched rune positions in Name
	score int
}

// Model is the sessionizer popup.
type Model struct {
	b       Backend
	st      *style.Styles
	now     func() time.Time
	all     []sessionizer.Entry
	rows    []row
	cur     int
	filter  split.Filter
	preview []string
	ticks   int
	loadErr error
	cfgErr  error
	w, h    int
	result  Result
	quit    bool
}

// relistEvery is how many Ticks pass between re-listings (the directory
// walk is the expensive part; the preview refreshes on every Tick).
const relistEvery = 8

// New loads the entries and returns the model, typing into the filter.
func New(o Options) Model {
	m := Model{b: o.Backend, st: o.Styles, now: o.Now, cfgErr: o.ConfigErr}
	if m.now == nil {
		m.now = time.Now
	}
	m.filter.Start()
	m.all, m.loadErr = m.b.Entries()
	m.refresh()
	return m
}

// Result is the entry chosen, if any.
func (m Model) Result() Result { return m.result }

// Rows is the entries listed, in order (for tests).
func (m Model) Rows() []sessionizer.Entry {
	out := make([]sessionizer.Entry, len(m.rows))
	for i, r := range m.rows {
		out[i] = r.Entry
	}
	return out
}

// Cursor is the selected row's index.
func (m Model) Cursor() int { return m.cur }

// refresh rebuilds rows from all: the fuzzy matches of the filter on the
// name, best first (ties keep the list order: sessions, then dirs).
func (m *Model) refresh() {
	var rows []row
	for _, e := range m.all {
		if pos, score, ok := split.Fuzzy(m.filter.Text, e.Name); ok {
			rows = append(rows, row{Entry: e, pos: pos, score: score})
		}
	}
	if m.filter.Text != "" {
		slices.SortStableFunc(rows, func(a, b row) int {
			if a.score != b.score {
				return b.score - a.score
			}
			return len(a.Name) - len(b.Name)
		})
	}
	m.rows = rows
	m.cur = max(0, min(m.cur, len(m.rows)-1))
	m.capture()
}

func (m Model) sel() (sessionizer.Entry, bool) {
	if m.cur >= 0 && m.cur < len(m.rows) {
		return m.rows[m.cur].Entry, true
	}
	return sessionizer.Entry{}, false
}

// capture refreshes the preview of the selected session's active pane.
func (m *Model) capture() {
	m.preview = nil
	if m.h == 0 {
		return // no size yet: nothing to show, and no tmux call before the first frame
	}
	if e, ok := m.sel(); ok && e.Pane != "" {
		m.preview = m.b.Capture(e.Pane, m.previewHeight())
	}
}

func (m Model) Init() tea.Cmd { return split.Every() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.capture()
	case split.Tick:
		if m.ticks++; m.ticks%relistEvery == 0 {
			if all, err := m.b.Entries(); err == nil {
				m.all, m.loadErr = all, nil
				m.refresh()
				return m, split.Every()
			}
		}
		m.capture()
		return m, split.Every()
	case tea.KeyMsg:
		var cmd tea.Cmd
		m, cmd = m.key(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) close() (Model, tea.Cmd) {
	m.quit = true
	return m, tea.Quit
}

func (m Model) key(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.close()
	case "esc":
		if m.filter.Text != "" {
			m.filter.Clear()
			m.filter.Start()
			m.refresh()
			return m, nil
		}
		return m.close()
	case "enter", "ctrl+y":
		if e, ok := m.sel(); ok {
			m.result.Switch = &e
			return m.close()
		}
		return m, nil
	case "down", "ctrl+n":
		m.cur = min(len(m.rows)-1, m.cur+1)
	case "up", "ctrl+p":
		m.cur = max(0, m.cur-1)
	default:
		if _, changed := m.filter.Key(msg); changed {
			m.cur = 0
			m.refresh()
		}
		return m, nil
	}
	m.cur = max(0, m.cur)
	m.capture()
	return m, nil
}

// ---- view

func (m Model) View() string {
	if m.w == 0 || m.quit {
		return ""
	}
	inner := split.Inner(m.w)
	lw, rw := split.Columns(inner)
	above := m.above(inner)
	bh := split.BodyHeight(m.h, len(above))
	return split.Layout{
		Title:  "sessions",
		Header: m.header(),
		Left:   m.list(lw, bh),
		Right:  m.panel(rw, bh),
		Above:  above,
		Footer: m.footer(),
	}.Render(m.st, m.w, m.h)
}

func (m Model) row(r row, w int, selected bool) string {
	icon := "○"
	if r.Type == "session" {
		icon = "●"
	}
	base, hl, muted := m.st.Fg("fg"), m.st.Fg("accent").Bold(true), m.st.Muted()
	if selected {
		base, hl, muted = m.st.On("fg", "bg_raised"), m.st.On("accent", "bg_raised").Bold(true), m.st.On("fg_muted", "bg_raised")
	}
	content := base.Render(icon+" ") + split.Highlight(r.Name, r.pos, base, hl)
	if r.Agents > 0 {
		n := fmt.Sprintf("  %d agents", r.Agents)
		if r.Agents == 1 {
			n = "  1 agent"
		}
		content += muted.Render(n)
	}
	return m.st.Row(selected, content, w)
}

func (m Model) list(w, h int) []string {
	if len(m.rows) == 0 {
		msg := "no sessions or directories"
		switch {
		case m.loadErr != nil:
			msg = "can't list: " + m.loadErr.Error()
		case m.filter.Text != "":
			msg = "no matches"
		}
		return []string{m.st.Muted().Render("  " + msg)}
	}
	var out []string
	for i := split.Offset(m.cur, h); i < len(m.rows) && len(out) < h; i++ {
		out = append(out, m.row(m.rows[i], w, i == m.cur))
	}
	return out
}

// pathLines is the selected entry's path hard-wrapped to the right column.
func (m Model) pathLines(w int) []string {
	e, _ := m.sel()
	return strings.Split(ansi.Hardwrap(e.Path, w, true), "\n")
}

// previewHeight is the panel rows left under the path, the what-⏎-does line
// and the blank line after each.
func (m Model) previewHeight() int {
	inner := split.Inner(m.w)
	_, rw := split.Columns(inner)
	return max(1, split.BodyHeight(m.h, len(m.above(inner)))-len(m.pathLines(rw))-3)
}

// panel is the right column: the selected entry's full path, what ⏎ does,
// then the session's live preview or the directory's contents.
func (m Model) panel(w, h int) []string {
	e, ok := m.sel()
	if !ok || w <= 0 {
		return nil
	}
	var out []string
	for _, l := range m.pathLines(w) {
		out = append(out, m.st.Text().Render(l))
	}
	out = append(out, "")
	if e.Type == "session" {
		out = append(out, m.st.Muted().Render("open session · ⏎ switches to it"), "")
		return append(out, split.Preview(m.preview, w, max(0, h-len(out)))...)
	}
	name := sessionizer.SessionName(e.Path)
	verb := "⏎ creates session "
	if m.hasSession(name) {
		verb = "⏎ switches to session "
	}
	out = append(out, m.st.Muted().Render(verb)+m.st.Accent().Render(name), "")
	return append(out, m.listing(e.Path, max(0, h-len(out)))...)
}

func (m Model) hasSession(name string) bool {
	for _, e := range m.all {
		if e.Type == "session" && e.Name == name {
			return true
		}
	}
	return false
}

// listing is the first n entries of dir, directories first with a "/".
func (m Model) listing(dir string, n int) []string {
	ents, err := os.ReadDir(dir)
	if err != nil || n <= 0 {
		return nil
	}
	slices.SortStableFunc(ents, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return 0
	})
	var out []string
	for _, ent := range ents {
		if len(out) == n {
			out[n-1] = m.st.Muted().Render(fmt.Sprintf("… %d more", len(ents)-n+1))
			break
		}
		if ent.IsDir() {
			out = append(out, m.st.Text().Render(ent.Name()+"/"))
		} else {
			out = append(out, m.st.Muted().Render(ent.Name()))
		}
	}
	return out
}

func (m Model) header() string {
	counts := fmt.Sprintf("%d sessions, %d dirs", m.sessionCount(), m.dirCount())
	if m.filter.Text != "" {
		counts = fmt.Sprintf("%d/%d", len(m.rows), len(m.all))
	}
	return m.filter.Prompt(m.st) + "   " + m.st.Accent().Render(counts)
}

func (m Model) sessionCount() int {
	count := 0
	for _, e := range m.all {
		if e.Type == "session" {
			count++
		}
	}
	return count
}

func (m Model) dirCount() int {
	count := 0
	for _, e := range m.all {
		if e.Type == "dir" {
			count++
		}
	}
	return count
}

// above is the config.toml error in red, wrapped to w cells (none if the
// file is fine).
func (m Model) above(w int) []string {
	if m.cfgErr == nil {
		return nil
	}
	var out []string
	for _, l := range style.Wrap(m.cfgErr.Error(), w) {
		out = append(out, m.st.Error().Render(l))
	}
	return out
}

const hints = "type to filter · C-n/C-p move · ⏎ switch/launch · esc clear/close"

func (m Model) footer() string {
	return m.st.Muted().Render(hints)
}
