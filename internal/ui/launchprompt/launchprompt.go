// Package launchprompt is the launch prompt: the visible [[agent]] entries,
// each with its key in accent, and the red `config.toml: <error>` line when
// the file is broken. Pressing an agent's key chooses it; q or Esc closes.
//
// Prompt is the reusable part: the picker's `n` mode shows Prompt.Inline in
// its footer and calls Prompt.Pick on the next key. Model is the standalone
// popup (`herdmux launch`, prefix o).
package launchprompt

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/config"
	"herdmux/internal/ui/style"
)

// Prompt is what the launch prompt shows.
type Prompt struct {
	Agents []config.Agent // the visible entries (config.Config.Visible), menu order
	Err    error          // the config.toml error, if any (shown in red)
	Where  string         // where it launches, e.g. "~/src/api" (ShortDir)
}

// Pick returns the agent whose key is key (a tea.KeyMsg's String()).
func (p Prompt) Pick(key string) (config.Agent, bool) {
	for _, a := range p.Agents {
		if a.Key == key {
			return a, true
		}
	}
	return config.Agent{}, false
}

// ShortDir shows dir with home as "~".
func ShortDir(dir, home string) string {
	if home != "" && home != "/" {
		home = filepath.Clean(home)
		if dir == home {
			return "~"
		}
		if strings.HasPrefix(dir, home+"/") {
			return "~" + dir[len(home):]
		}
	}
	return dir
}

// Inline is the one-line form for another popup's footer:
// "launch: c claude · o opencode · p pi", keys in accent.
func (p Prompt) Inline(st *style.Styles) string {
	if len(p.Agents) == 0 {
		return st.Accent().Render("launch: ") + st.Muted().Render("no agents found on PATH")
	}
	parts := make([]string, len(p.Agents))
	for i, a := range p.Agents {
		parts[i] = st.Accent().Render(a.Key) + st.Text().Render(" "+a.Name)
	}
	return st.Accent().Render("launch: ") + strings.Join(parts, st.Muted().Render(" · "))
}

// ErrorLines is the config error in red, wrapped to w cells (none if the
// file is fine).
func (p Prompt) ErrorLines(st *style.Styles, w int) []string {
	if p.Err == nil {
		return nil
	}
	var out []string
	for _, l := range style.Wrap(p.Err.Error(), w) {
		out = append(out, st.Error().Render(l))
	}
	return out
}

// Layout constants of the standalone prompt.
const (
	minWidth = 36
	maxWidth = 72
	gutter   = 1 // blank cells inside the frame on each side
)

func (p Prompt) nameWidth() int {
	n := 0
	for _, a := range p.Agents {
		n = max(n, ansi.StringWidth(a.Name))
	}
	return n
}

func (p Prompt) rowWidth() int {
	w := 0
	for _, a := range p.Agents {
		rw := 1 + 1 + 2 + p.nameWidth() // indent, key, gap, name
		if a.Command != a.Name {
			rw += 2 + ansi.StringWidth(a.Command)
		}
		w = max(w, rw)
	}
	return w
}

// Size is the standalone popup's size in cells, frame included.
func (p Prompt) Size() (w, h int) {
	content := p.rowWidth()
	if p.Err != nil {
		content = max(content, ansi.StringWidth(p.Err.Error()))
	}
	w = min(maxWidth, max(minWidth, content+2+2*gutter))
	return w, len(p.body(nil, w-2)) + 2
}

// body is the lines inside the frame, inner cells wide. With st nil it only
// counts lines (for Size).
func (p Prompt) body(st *style.Styles, inner int) []string {
	w := inner - 2*gutter
	pad := strings.Repeat(" ", gutter)
	if st == nil {
		st = style.New(style.Palette{})
	}
	var lines []string
	add := func(s string) { lines = append(lines, pad+s) }
	where := "launch here"
	if p.Where != "" {
		where = "launch in " + p.Where
	}
	add(st.Muted().Render(ansi.Truncate(where, w, "…")))
	add("")
	if len(p.Agents) == 0 {
		add(st.Muted().Render(" no agents found on PATH"))
	}
	nw := p.nameWidth()
	for _, a := range p.Agents {
		row := " " + st.Accent().Render(a.Key) + "  " + st.Text().Render(a.Name) + strings.Repeat(" ", nw-ansi.StringWidth(a.Name))
		if a.Command != a.Name {
			row += "  " + st.Muted().Render(a.Command)
		}
		add(ansi.Truncate(row, w, "…"))
	}
	add("")
	for _, l := range p.ErrorLines(st, w) {
		add(l)
	}
	hint := "press a key to launch · q close"
	if len(p.Agents) == 0 {
		hint = "q close"
	}
	add(st.Muted().Render(hint))
	return lines
}

// Model is the standalone launch prompt popup.
type Model struct {
	P      Prompt
	st     *style.Styles
	w, h   int
	chosen config.Agent
	ok     bool
}

// New returns the popup model; it sizes itself from the window.
func New(p Prompt, st *style.Styles) Model {
	w, h := p.Size()
	return Model{P: p, st: st, w: w, h: h}
}

// Chosen is the agent picked before the program quit, if any.
func (m Model) Chosen() (config.Agent, bool) { return m.chosen, m.ok }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		default:
			if a, ok := m.P.Pick(k); ok {
				m.chosen, m.ok = a, true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.ok {
		return "" // chosen: the popup is closing
	}
	return m.st.Frame("launch", m.w, m.h, m.P.body(m.st, m.w-2))
}
