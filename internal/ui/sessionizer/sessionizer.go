// Package sessionizer is the session switcher / project launcher popup
// (split layout with the session/dir list on the left, no right panel).
//
// Keys: j/k and arrows move, ⏎ switches/launches, / filters, q/Esc close.
package sessionizer

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdmux/internal/sessionizer"
	"herdmux/internal/ui/split"
	"herdmux/internal/ui/style"
)

// Backend is what the model needs from tmux.
type Backend interface {
	Entries() ([]sessionizer.Entry, error) // list sessions and dirs
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

// Model is the sessionizer popup.
type Model struct {
	b       Backend
	st      *style.Styles
	now     func() time.Time
	all     []sessionizer.Entry
	rows    []sessionizer.Entry
	cur     int
	filter  split.Filter
	msg     string
	msgErr  bool
	loadErr error
	cfgErr  error
	w, h    int
	result  Result
	quit    bool
}

// New loads the entries and returns the model.
func New(o Options) Model {
	m := Model{b: o.Backend, st: o.Styles, now: o.Now, cfgErr: o.ConfigErr}
	if m.now == nil {
		m.now = time.Now
	}
	m.all, m.loadErr = m.b.Entries()
	m.refresh(false)
	return m
}

// Result is the entry chosen, if any.
func (m Model) Result() Result { return m.result }

// Rows is the entries listed, in order (for tests).
func (m Model) Rows() []sessionizer.Entry { return m.rows }

// Cursor is the selected row's index.
func (m Model) Cursor() int { return m.cur }

// refresh rebuilds rows from all, applying the filter.
func (m *Model) refresh(keep bool) {
	var rows []sessionizer.Entry
	for _, e := range m.all {
		// Match on name and type.
		if split.Match(m.filter.Text, e.Name, e.Type) {
			rows = append(rows, e)
		}
	}
	m.rows = rows
	m.cur = max(0, min(m.cur, len(m.rows)-1))
}

func (m Model) sel() (sessionizer.Entry, bool) {
	if m.cur >= 0 && m.cur < len(m.rows) {
		return m.rows[m.cur], true
	}
	return sessionizer.Entry{}, false
}

func (m Model) Init() tea.Cmd { return split.Every() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case split.Tick:
		if all, err := m.b.Entries(); err == nil {
			m.all, m.loadErr = all, nil
			m.refresh(true)
		}
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
	if used, changed := m.filter.Key(msg); used {
		if changed {
			m.refresh(true)
		}
		return m, nil
	}
	m.msg = ""
	k := msg.String()
	switch k {
	case "q", "ctrl+c":
		return m.close()
	case "esc":
		if m.filter.Text != "" {
			m.filter.Clear()
			m.refresh(true)
			return m, nil
		}
		return m.close()
	case "j", "down":
		m.cur = min(len(m.rows)-1, m.cur+1)
	case "k", "up":
		m.cur = max(0, m.cur-1)
	case "enter":
		if e, ok := m.sel(); ok {
			m.result.Switch = &e
			return m.close()
		}
	case "/":
		m.filter.Start()
	}
	m.cur = max(0, m.cur)
	return m, nil
}

// ---- view

func (m Model) View() string {
	if m.w == 0 || m.quit {
		return ""
	}
	inner := split.Inner(m.w)
	lw, _ := split.Columns(inner)
	above := m.above(inner)
	bh := split.BodyHeight(m.h, len(above))
	return split.Layout{
		Title:  "sessions",
		Header: m.header(),
		Left:   m.list(lw, bh),
		Right:  nil,
		Above:  above,
		Footer: m.footer(),
	}.Render(m.st, m.w, m.h)
}

func (m Model) row(e sessionizer.Entry, w int, selected bool) string {
	icon := "○"
	slot := "fg"
	if e.Type == "session" {
		icon = "●"
	}
	seg := func(s string) string {
		if selected {
			return m.st.On(slot, "bg_raised").Render(s)
		}
		return m.st.Fg(slot).Render(s)
	}
	content := seg(icon+" ") + seg(e.Name)
	if e.Agents > 0 {
		n := fmt.Sprintf("  %d agents", e.Agents)
		if e.Agents == 1 {
			n = "  1 agent"
		}
		content += m.st.Muted().Render(n)
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

func (m Model) header() string {
	h := m.st.Accent().Render(fmt.Sprintf("%d sessions, %d dirs", m.sessionCount(), m.dirCount()))
	if p := m.filter.Prompt(m.st); p != "" {
		h = p + "   " + h
	}
	return h
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

const hints = "j/k move · ⏎ switch/launch · / filter · q close"

func (m Model) footer() string {
	if m.msg != "" {
		if m.msgErr {
			return m.st.Error().Render(m.msg)
		}
		return m.st.Fg("state_done").Render(m.msg)
	}
	if m.filter.Typing {
		return m.st.Muted().Render("type to filter · ⏎ keep · esc clear")
	}
	return m.st.Muted().Render(hints)
}
