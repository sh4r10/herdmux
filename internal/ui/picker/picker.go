// Package picker is the agent picker popup: the split layout
// (internal/ui/split) with every agent on the left and a live preview of the
// selected agent's pane on the right.
//
// Keys: j/k and the arrows move, ⏎ jumps, / filters, Tab toggles the scope
// (all sessions ↔ this session), s toggles the order (by place ↔ Attention
// first), n launches (the launch prompt's keys), x stops the agent after a
// y confirm, q/Esc close.
//
// Model talks to tmux only through a Backend (TmuxBackend in a popup, a fake
// in tests). A jump or a launch is returned by Result after the program
// quits, and the verb carries it out.
package picker

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/ui/launchprompt"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// Settings are the two toggles remembered between opens.
type Settings struct {
	ThisSession    bool // Tab: only the current session's agents (default: all sessions)
	AttentionFirst bool // s: Attention first (default: by place)
}

// Backend is what the model needs from tmux.
type Backend interface {
	Agents() ([]state.Agent, error)          // every agent, tmux order (state.List)
	Capture(pane string, lines int) []string // the preview (split.Capture)
	Stop(a state.Agent) error                // stop the agent, keep the pane (state.Stop)
	Save(s Settings)                         // remember the toggles
}

// Options configure New.
type Options struct {
	Backend  Backend
	Styles   *style.Styles
	Session  string              // the client's session name: listed first, and Tab's scope
	Settings Settings            // as saved
	Prompt   launchprompt.Prompt // what `n` offers (config.toml's visible agents, its error)
	Now      func() time.Time    // nil: time.Now
}

// Result is what the user chose; the verb acts on it after the program quits.
type Result struct {
	Jump   *state.Agent  // ⏎: switch the client to this agent's pane
	Launch *config.Agent // n + key: launch it here and jump to it
}

type mode int

const (
	modeList mode = iota
	modeLaunch
	modeStop
)

// Model is the picker.
type Model struct {
	b       Backend
	st      *style.Styles
	now     func() time.Time
	session string
	set     Settings
	prompt  launchprompt.Prompt

	all, rows []state.Agent
	cur       int
	filter    split.Filter
	mode      mode
	msg       string
	msgErr    bool
	loadErr   error

	w, h    int
	preview []string
	result  Result
	quit    bool
}

// New loads the agents and returns the picker.
func New(o Options) Model {
	m := Model{b: o.Backend, st: o.Styles, now: o.Now, session: o.Session, set: o.Settings, prompt: o.Prompt}
	if m.now == nil {
		m.now = time.Now
	}
	m.all, m.loadErr = m.b.Agents()
	m.refresh(false)
	return m
}

// Result is the jump or launch chosen, if any.
func (m Model) Result() Result { return m.result }

// Rows is the agents listed, in order (for tests).
func (m Model) Rows() []state.Agent { return m.rows }

// Cursor is the selected row's index.
func (m Model) Cursor() int { return m.cur }

// Settings are the current toggles.
func (m Model) Settings() Settings { return m.set }

// Place is an agent's place as the picker shows it: "session:index window".
func Place(a state.Agent) string {
	return fmt.Sprintf("%s:%d %s", a.Session, a.WindowIndex, a.WindowName)
}

// Order sorts agents for the picker. By place: the current session first,
// then the other sessions in the order given (tmux's), windows by index, and
// panes in the order given. Attention first: needs-input Attention, then
// done Attention, each oldest first (as next-attention picks them), then
// everything else by place.
func Order(agents []state.Agent, session string, attentionFirst bool) []state.Agent {
	rank := map[string]int{session: 0}
	for _, a := range agents {
		if _, ok := rank[a.Session]; !ok {
			rank[a.Session] = len(rank)
		}
	}
	att := func(a state.Agent) int {
		switch a.Attention {
		case state.NeedsInput:
			return 0
		case state.Done:
			return 1
		}
		return 2
	}
	out := slices.Clone(agents)
	slices.SortStableFunc(out, func(a, b state.Agent) int {
		if attentionFirst {
			if d := att(a) - att(b); d != 0 {
				return d
			}
			if att(a) < 2 && a.Since != b.Since {
				return compare(a.Since, b.Since)
			}
		}
		if d := rank[a.Session] - rank[b.Session]; d != 0 {
			return d
		}
		return a.WindowIndex - b.WindowIndex
	})
	return out
}

func compare(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// refresh rebuilds rows from all. With keep, the selection follows its pane
// if that is still listed; otherwise (and after a stop) the cursor stays at
// its index, clamped to the list.
func (m *Model) refresh(keep bool) {
	var sel string
	if a, ok := m.sel(); ok && keep {
		sel = a.Pane
	}
	var rows []state.Agent
	for _, a := range m.all {
		if m.set.ThisSession && a.Session != m.session {
			continue
		}
		if !split.Match(m.filter.Text, a.Agent, Place(a), a.State) {
			continue
		}
		rows = append(rows, a)
	}
	m.rows = Order(rows, m.session, m.set.AttentionFirst)
	m.cur = max(0, min(m.cur, len(m.rows)-1))
	if i := slices.IndexFunc(m.rows, func(a state.Agent) bool { return a.Pane == sel }); sel != "" && i >= 0 {
		m.cur = i
	}
}

func (m Model) sel() (state.Agent, bool) {
	if m.cur >= 0 && m.cur < len(m.rows) {
		return m.rows[m.cur], true
	}
	return state.Agent{}, false
}

// bodyHeight is the list/preview height for the current mode.
func (m Model) bodyHeight() int {
	return split.BodyHeight(m.h, len(m.above(split.Inner(m.w))))
}

// recapture refreshes the preview of the selected pane: only that pane, only
// the lines the panel shows (its header takes one).
func (m *Model) recapture() {
	a, ok := m.sel()
	if !ok || m.h == 0 {
		m.preview = nil
		return
	}
	m.preview = m.b.Capture(a.Pane, m.bodyHeight()-1)
}

func (m Model) Init() tea.Cmd { return split.Every() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.recapture()
	case split.Tick:
		if all, err := m.b.Agents(); err == nil {
			m.all, m.loadErr = all, nil
			m.refresh(true)
		}
		m.recapture()
		return m, split.Every()
	case tea.KeyMsg:
		var cmd tea.Cmd
		m, cmd = m.key(msg)
		if !m.quit {
			m.recapture() // the newly selected pane shows at once, not on the next tick
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) close() (Model, tea.Cmd) {
	m.quit = true
	return m, tea.Quit
}

func (m Model) key(msg tea.KeyMsg) (Model, tea.Cmd) {
	k := msg.String()
	switch m.mode {
	case modeLaunch:
		m.mode = modeList
		if a, ok := m.prompt.Pick(k); ok {
			m.result.Launch = &a
			return m.close()
		}
		return m, nil
	case modeStop:
		m.mode = modeList
		a, ok := m.sel()
		if !ok || k != "y" {
			return m, nil
		}
		if err := m.b.Stop(a); err != nil {
			m.msg, m.msgErr = fmt.Sprintf("could not stop %s in %s: %v", a.Agent, Place(a), err), true
		} else {
			m.msg, m.msgErr = fmt.Sprintf("stopped %s in %s (pane kept)", a.Agent, Place(a)), false
		}
		if all, err := m.b.Agents(); err == nil {
			m.all = all
		}
		m.refresh(false) // the cursor stays on the same row, clamped
		return m, nil
	}

	if used, changed := m.filter.Key(msg); used {
		if changed {
			m.refresh(true)
		}
		return m, nil
	}
	m.msg = ""
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
	case "j", "down", "ctrl+n":
		m.cur = min(len(m.rows)-1, m.cur+1)
	case "k", "up", "ctrl+p":
		m.cur = max(0, m.cur-1)
	case "enter", "ctrl+y":
		if a, ok := m.sel(); ok {
			m.result.Jump = &a
			return m.close()
		}
	case "tab":
		m.set.ThisSession = !m.set.ThisSession
		m.b.Save(m.set)
		m.refresh(true)
	case "s":
		m.set.AttentionFirst = !m.set.AttentionFirst
		m.b.Save(m.set)
		m.refresh(true)
	case "/":
		m.filter.Start()
	case "n":
		m.mode = modeLaunch
	case "x":
		if _, ok := m.sel(); ok {
			m.mode = modeStop
		}
	}
	m.cur = max(0, m.cur)
	return m, nil
}

// ---- view

// Column widths of a row (the place column takes the rest).
const (
	nameW  = 8  // opencode
	stateW = 11 // needs-input
	ageW   = 4
	fixedW = 1 + 2 + nameW + 2 + 2 + stateW + 1 + ageW // bar, icon, gaps, name, state, age
)

func (m Model) View() string {
	if m.w == 0 || m.quit {
		return "" // not sized yet, or closing
	}
	inner := split.Inner(m.w)
	lw, rw := split.Columns(inner)
	above := m.above(inner)
	bh := split.BodyHeight(m.h, len(above))
	return split.Layout{
		Title:  "agents",
		Header: m.header(),
		Left:   m.list(lw, bh),
		Right:  m.previewPanel(rw, bh),
		Above:  above,
		Footer: m.footer(),
	}.Render(m.st, m.w, m.h)
}

// cell cuts s (plain text) with "…" or pads it to w cells.
func cell(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func attentionSlot(att string) string {
	if att == state.NeedsInput {
		return "attention_needs_input"
	}
	return "attention_done"
}

// row is ` icon agent  session:index window  state  age`. Only a selected
// row has a background (bg_raised, with the accent bar). Attention draws the
// icon and the name bold in its colour, and nothing else changes.
func (m Model) row(a state.Agent, w int, selected bool) string {
	seg := func(slot string, bold bool) lipgloss.Style {
		if selected {
			return m.st.On(slot, "bg_raised").Bold(bold)
		}
		return m.st.Fg(slot).Bold(bold)
	}
	iconSlot, nameSlot, attBold := style.StateSlot(a.State), "fg", false
	if a.Attention != "" {
		iconSlot, nameSlot, attBold = attentionSlot(a.Attention), attentionSlot(a.Attention), true
	}
	placeW := max(4, w-fixedW)
	age := style.Age(a.Since, m.now())
	content := seg(iconSlot, attBold).Render(style.StateIcon(a.State)+" ") +
		seg(nameSlot, attBold || selected).Render(cell(a.Agent, nameW)) +
		seg("fg", selected).Render("  "+cell(Place(a), placeW)+"  "+cell(a.State, stateW)+" "+strings.Repeat(" ", max(0, ageW-len(age)))+age)
	return m.st.Row(selected, content, w)
}

func (m Model) list(w, h int) []string {
	if len(m.rows) == 0 {
		msg := "no agents"
		switch {
		case m.loadErr != nil:
			msg = "can't list agents: " + m.loadErr.Error()
		case m.filter.Text != "":
			msg = "no agents match"
		case m.set.ThisSession && len(m.all) > 0:
			msg = "no agents in this session · tab for all"
		}
		return []string{m.st.Muted().Render("  " + msg)}
	}
	var out []string
	for i := split.Offset(m.cur, h); i < len(m.rows) && len(out) < h; i++ {
		out = append(out, m.row(m.rows[i], w, i == m.cur))
	}
	return out
}

// previewPanel is `icon agent · session:index window · state age`, then the
// pane's last lines.
func (m Model) previewPanel(w, h int) []string {
	a, ok := m.sel()
	if !ok {
		return nil
	}
	head := m.st.Fg(style.StateSlot(a.State)).Render(style.StateIcon(a.State)+" ") +
		m.st.Text().Bold(true).Render(a.Agent) +
		m.st.Muted().Render(" · "+Place(a)+" · "+strings.TrimSpace(a.State+" "+style.Age(a.Since, m.now())))
	return append([]string{head}, split.Preview(m.preview, w, h-1)...)
}

func (m Model) header() string {
	scope := "all sessions"
	if m.set.ThisSession {
		scope = "this session (" + m.session + ")"
	}
	order := "by place"
	if m.set.AttentionFirst {
		order = "attention first"
	}
	h := m.st.Accent().Render(scope) + m.st.Muted().Render(" · "+order)
	if p := m.filter.Prompt(m.st); p != "" {
		h = p + "   " + h
	}
	return h
}

// above is the launch prompt's red config.toml error while `n` is pending.
func (m Model) above(w int) []string {
	if m.mode != modeLaunch {
		return nil
	}
	return m.prompt.ErrorLines(m.st, w)
}

const hints = "j/k move · ⏎ jump · / filter · tab scope · s sort · n launch · x stop · q close"

func (m Model) footer() string {
	switch {
	case m.mode == modeLaunch:
		return m.prompt.Inline(m.st) + m.st.Muted().Render(" · other key cancels")
	case m.mode == modeStop:
		a, _ := m.sel()
		return m.st.Error().Render(fmt.Sprintf(" stop %s in %s? (pane stays)", a.Agent, Place(a))) +
			m.st.Muted().Render("  y stop · other key cancels")
	case m.msg != "" && m.msgErr:
		return m.st.Error().Render(m.msg)
	case m.msg != "":
		return m.st.Fg("state_done").Render(m.msg)
	case m.filter.Typing:
		return m.st.Muted().Render("type to filter · ⏎ keep · esc clear")
	}
	return m.st.Muted().Render(hints)
}
