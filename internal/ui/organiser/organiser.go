// Package organiser is the window organiser popup: the split layout
// (internal/ui/split) with the current session's windows on the left and a
// live preview of the selected window's active pane on the right.
//
// Keys: j/k and the arrows, gg/G move; space grabs the selected window and
// j/k then carry it, each step a live swap-window (space or ⏎ drops it, Esc
// puts it back); u undoes drops and renames; r renames inline; x kills after
// a y confirm; ⏎ jumps and closes; q/Esc close.
//
// The window you were on stays current throughout and follows its contents
// to its new index, and tmux's last window (prefix l) is kept (see
// TmuxBackend.Swap). Model talks to tmux only through a Backend (TmuxBackend
// in a popup, a fake in tests). A jump is returned by Result after the
// program quits, and the verb carries it out.
package organiser

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// Window is one row: a window of the session, as its tab shows it.
type Window struct {
	ID        string   // #{window_id}, @N
	Index     int      // #{window_index}
	Name      string   // #{window_name}
	Panes     int      // #{window_panes}
	Pane      string   // the active pane (the preview)
	Command   string   // the active pane's #{pane_current_command}
	Active    bool     // the session's current window
	Last      bool     // tmux's last window (prefix l)
	States    []string // the agents' states in pane order: the tab's icons
	Attention string   // the window's Attention: needs-input beats done
}

// Backend is what the model needs from tmux. Window ids are @N; every call
// acts in the organiser's session.
type Backend interface {
	Windows() ([]Window, error)              // the session's windows by index
	Capture(pane string, lines int) []string // the preview (split.Capture)
	// Swap exchanges the places of windows a and b, then makes current the
	// session's current window again and last its last window ("" when
	// there is none).
	Swap(a, b, current, last string) error
	// Rename renames a window and returns its window-local
	// automatic-rename value from before ("" when it had none), which
	// rename-window turns off.
	Rename(id, name string) (auto string, err error)
	// Unrename gives a window back its name and automatic-rename value.
	Unrename(id, name, auto string) error
	Kill(id string) error
}

// Options configure New.
type Options struct {
	Backend Backend
	Styles  *style.Styles
	Session string // the session's name, for the header
}

// Result is what the user chose; the verb acts on it after the program quits.
type Result struct {
	Jump string // ⏎: select this window (@N)
}

type mode int

const (
	modeList mode = iota
	modeRename
	modeKill
)

type msgKind int

const (
	msgHint msgKind = iota // fg_muted
	msgOK                  // state_done
	msgErr                 // attention_needs_input
)

// undo is a committed drop (the id order before it) or a rename (the old
// name and automatic-rename value).
type undo struct {
	order          []string
	id, name, auto string
}

// Model is the organiser.
type Model struct {
	b       Backend
	st      *style.Styles
	session string

	rows      []Window
	cur       int
	curID     string // the session's current window: kept current through every swap
	lastID    string // tmux's last window: kept last through every swap
	grab      string // the grabbed window's id; "" when not grabbing
	grabStart []string
	undos     []undo
	mode      mode
	edit      []rune
	pendingG  bool
	msg       string
	msgKind   msgKind
	loadErr   error

	w, h    int
	preview []string
	result  Result
	quit    bool
}

// New loads the windows and returns the organiser, with the cursor on the
// current window.
func New(o Options) Model {
	m := Model{b: o.Backend, st: o.Styles, session: o.Session}
	m.refresh()
	if i := m.index(m.curID); i >= 0 {
		m.cur = i
	}
	return m
}

// Result is the jump chosen, if any.
func (m Model) Result() Result { return m.result }

// Rows is the windows listed, by index (for tests).
func (m Model) Rows() []Window { return m.rows }

// Cursor is the selected row's index.
func (m Model) Cursor() int { return m.cur }

// Grabbed is the grabbed window's id, "" when nothing is grabbed.
func (m Model) Grabbed() string { return m.grab }

func (m Model) ids() []string {
	out := make([]string, len(m.rows))
	for i, w := range m.rows {
		out[i] = w.ID
	}
	return out
}

func (m Model) index(id string) int {
	return slices.IndexFunc(m.rows, func(w Window) bool { return w.ID == id })
}

func (m Model) sel() (Window, bool) {
	if m.cur >= 0 && m.cur < len(m.rows) {
		return m.rows[m.cur], true
	}
	return Window{}, false
}

// refresh reloads the windows. The selection follows its window (the
// grabbed one while grabbing); if it is gone, the cursor stays at its
// index, clamped. The current and last windows are tmux's.
func (m *Model) refresh() {
	selID := m.grab
	if w, ok := m.sel(); ok && selID == "" {
		selID = w.ID
	}
	rows, err := m.b.Windows()
	if err != nil {
		m.loadErr = err
		return
	}
	m.rows, m.loadErr = rows, nil
	m.curID, m.lastID = "", ""
	for _, w := range rows {
		if w.Active {
			m.curID = w.ID
		}
		if w.Last {
			m.lastID = w.ID
		}
	}
	m.cur = max(0, min(m.cur, len(m.rows)-1))
	if i := m.index(selID); i >= 0 {
		m.cur = i
	}
}

// bodyHeight is the list/preview height.
func (m Model) bodyHeight() int { return split.BodyHeight(m.h, 0) }

// recapture refreshes the preview of the selected window's active pane:
// only that pane, only the lines the panel shows (its header takes one).
func (m *Model) recapture() {
	w, ok := m.sel()
	if !ok || m.h == 0 {
		m.preview = nil
		return
	}
	m.preview = m.b.Capture(w.Pane, m.bodyHeight()-1)
}

func (m *Model) say(kind msgKind, format string, args ...any) {
	m.msg, m.msgKind = fmt.Sprintf(format, args...), kind
}

// swap is one live step: a and b exchange places, the current window stays
// current and the last window stays last.
func (m *Model) swap(a, b string) {
	if err := m.b.Swap(a, b, m.curID, m.lastID); err != nil {
		m.say(msgErr, "could not move the window: %v", err)
	}
	m.refresh()
}

// applyOrder puts the windows back into an id order with a sequence of
// swaps. Ids that are gone (killed) are skipped; windows not in it keep
// their relative order after it.
func (m *Model) applyOrder(target []string) {
	m.refresh()
	have := map[string]bool{}
	for _, id := range m.ids() {
		have[id] = true
	}
	var want []string
	in := map[string]bool{}
	for _, id := range target {
		if have[id] && !in[id] {
			want = append(want, id)
			in[id] = true
		}
	}
	for _, id := range m.ids() {
		if !in[id] {
			want = append(want, id)
		}
	}
	for i := range want {
		ids := m.ids()
		if i >= len(ids) {
			break
		}
		if ids[i] != want[i] {
			before := strings.Join(ids, " ")
			m.swap(want[i], ids[i])
			if strings.Join(m.ids(), " ") == before {
				return // the swap failed (say has the error); don't loop on it
			}
		}
	}
}

func (m Model) Init() tea.Cmd { return split.Every() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.recapture()
	case split.Tick:
		m.refresh()
		m.recapture()
		return m, split.Every()
	case tea.KeyMsg:
		var cmd tea.Cmd
		m, cmd = m.key(msg)
		if !m.quit {
			m.recapture() // the newly selected window shows at once, not on the next tick
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
	switch {
	case m.mode == modeRename:
		return m.renameKey(msg)
	case m.mode == modeKill:
		return m.killKey(msg.String())
	case m.grab != "":
		return m.grabKey(msg.String())
	}

	k := msg.String()
	m.msg = ""
	if m.pendingG {
		m.pendingG = false
		if k == "g" {
			m.cur = 0
			return m, nil
		}
	}
	switch k {
	case "q", "esc", "ctrl+c":
		return m.close()
	case "j", "down":
		m.cur = min(len(m.rows)-1, m.cur+1)
	case "k", "up":
		m.cur = max(0, m.cur-1)
	case "g":
		m.pendingG = true
	case "G":
		m.cur = len(m.rows) - 1
	case "enter":
		if w, ok := m.sel(); ok {
			m.result.Jump = w.ID
			return m.close()
		}
	case " ":
		if w, ok := m.sel(); ok {
			m.grab, m.grabStart = w.ID, m.ids()
		}
	case "r":
		if w, ok := m.sel(); ok {
			m.mode, m.edit = modeRename, []rune(w.Name)
		}
	case "x":
		if _, ok := m.sel(); ok {
			if len(m.rows) < 2 {
				m.say(msgErr, "can't kill the session's last window")
			} else {
				m.mode = modeKill
			}
		}
	case "u":
		m.undo()
	}
	m.cur = max(0, m.cur)
	return m, nil
}

// grabKey: j/k carry the grabbed window one step (live), space/⏎ drop it,
// Esc puts it back. Everything else (gg/G included) does nothing; Ctrl-C
// puts it back and closes.
func (m Model) grabKey(k string) (Model, tea.Cmd) {
	switch k {
	case "j", "down":
		if m.cur < len(m.rows)-1 {
			m.swap(m.grab, m.rows[m.cur+1].ID)
		}
	case "k", "up":
		if m.cur > 0 {
			m.swap(m.grab, m.rows[m.cur-1].ID)
		}
	case " ", "enter":
		if !slices.Equal(m.ids(), m.grabStart) {
			m.undos = append(m.undos, undo{order: m.grabStart})
		}
		m.grab, m.grabStart = "", nil
	case "esc", "ctrl+c":
		m.applyOrder(m.grabStart)
		m.grab, m.grabStart = "", nil
		if k == "ctrl+c" {
			return m.close()
		}
	}
	return m, nil
}

func (m Model) renameKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	w, ok := m.sel()
	if !ok {
		m.mode = modeList
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.mode, m.msg = modeList, ""
	case tea.KeyCtrlC:
		return m.close()
	case tea.KeyEnter:
		name := string(m.edit)
		if strings.TrimSpace(name) == "" {
			m.say(msgErr, "name can't be empty")
			return m, nil
		}
		m.mode, m.msg = modeList, ""
		if name == w.Name {
			return m, nil
		}
		auto, err := m.b.Rename(w.ID, name)
		if err != nil {
			m.say(msgErr, "could not rename: %v", err)
		} else {
			m.undos = append(m.undos, undo{id: w.ID, name: w.Name, auto: auto})
		}
		m.refresh()
	case tea.KeyCtrlU:
		m.edit = nil
	case tea.KeyBackspace:
		if len(m.edit) > 0 {
			m.edit = m.edit[:len(m.edit)-1]
		}
	case tea.KeySpace:
		m.edit = append(m.edit, ' ')
	case tea.KeyRunes:
		if !msg.Alt {
			m.edit = append(m.edit, msg.Runes...)
		}
	}
	return m, nil
}

func (m Model) killKey(k string) (Model, tea.Cmd) {
	m.mode = modeList
	w, ok := m.sel()
	if !ok || k != "y" {
		return m, nil
	}
	if err := m.b.Kill(w.ID); err != nil {
		m.say(msgErr, "could not kill window %d %s: %v", w.Index, w.Name, err)
	} else {
		m.say(msgOK, "killed window %d %s", w.Index, w.Name)
	}
	m.refresh() // the cursor stays on the same row, clamped
	return m, nil
}

// undo takes back the last drop or rename. Kills can't be undone; a drop's
// order skips windows killed since.
func (m *Model) undo() {
	if len(m.undos) == 0 {
		m.say(msgHint, "nothing to undo")
		return
	}
	u := m.undos[len(m.undos)-1]
	m.undos = m.undos[:len(m.undos)-1]
	if u.order != nil {
		m.applyOrder(u.order)
		if m.msg == "" {
			m.say(msgOK, "undid move")
		}
		return
	}
	if m.index(u.id) < 0 {
		m.say(msgHint, "that window is gone")
		return
	}
	if err := m.b.Unrename(u.id, u.name, u.auto); err != nil {
		m.say(msgErr, "could not undo the rename: %v", err)
	} else {
		m.say(msgOK, "undid rename")
	}
	m.refresh()
}

// ---- view

func (m Model) View() string {
	if m.w == 0 || m.quit {
		return "" // not sized yet, or closing
	}
	lw, rw := split.Columns(split.Inner(m.w))
	bh := m.bodyHeight()
	return split.Layout{
		Title:  "windows",
		Header: m.header(),
		Left:   m.list(lw, bh),
		Right:  m.previewPanel(rw, bh),
		Footer: m.footer(),
	}.Render(m.st, m.w, m.h)
}

func attentionSlot(att string) string {
	if att == state.NeedsInput {
		return "attention_needs_input"
	}
	return "attention_done"
}

// icons is the tab's agent icons, each with a space after it. With
// Attention they are bold in its colour; otherwise each takes its state's.
// seg styles a segment in a slot (on the row's background).
func icons(w Window, seg func(slot string, bold bool) lipgloss.Style) string {
	var b strings.Builder
	for _, s := range w.States {
		slot, bold := style.StateSlot(s), false
		if w.Attention != "" {
			slot, bold = attentionSlot(w.Attention), true
		}
		b.WriteString(seg(slot, bold).Render(style.StateIcon(s) + " "))
	}
	return b.String()
}

// pluralPanes is "1 pane" or "N panes".
func pluralPanes(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return strconv.Itoa(n) + " panes"
}

// row is `▸ index  icons name`, as the window tab reads. Only the selected
// row has a background (bg_raised, with the accent bar); the grabbed row is
// filled with accent. Attention draws the icons and the name bold in its
// colour. The current window has a muted ▸.
func (m Model) row(win Window, w, idxW int, selected bool) string {
	grabbed := win.ID == m.grab
	seg := func(slot string, bold bool) lipgloss.Style {
		switch {
		case grabbed:
			return m.st.On("bg", "accent").Bold(true)
		case selected:
			return m.st.On(slot, "bg_raised").Bold(bold)
		}
		return m.st.Fg(slot).Bold(bold)
	}
	mark := " "
	if win.ID == m.curID {
		mark = "▸"
	}
	content := seg("fg_muted", false).Render(mark+" ") +
		seg("fg", selected).Render(fmt.Sprintf("%*d  ", idxW, win.Index)) +
		icons(win, seg)
	switch {
	case selected && m.mode == modeRename:
		content += m.st.On("fg", "bg_raised").Underline(true).Render(string(m.edit)) +
			m.st.On("accent", "bg_raised").Render("█")
	case win.Attention != "":
		content += seg(attentionSlot(win.Attention), true).Render(win.Name)
	default:
		content += seg("fg", selected).Render(win.Name)
	}
	if grabbed {
		return m.st.On("bg", "accent").Render(" ") + m.st.Pad(content, w-1, "accent")
	}
	return m.st.Row(selected, content, w)
}

func (m Model) list(w, h int) []string {
	if len(m.rows) == 0 {
		msg := "no windows"
		if m.loadErr != nil {
			msg = "can't list windows: " + m.loadErr.Error()
		}
		return []string{m.st.Muted().Render("  " + msg)}
	}
	idxW := 1
	for _, win := range m.rows {
		idxW = max(idxW, len(strconv.Itoa(win.Index)))
	}
	var out []string
	for i := split.Offset(m.cur, h); i < len(m.rows) && len(out) < h; i++ {
		out = append(out, m.row(m.rows[i], w, idxW, i == m.cur))
	}
	return out
}

// previewPanel is `icons index name · N panes · active pane: cmd`, then the
// active pane's last lines.
func (m Model) previewPanel(w, h int) []string {
	win, ok := m.sel()
	if !ok {
		return nil
	}
	plain := func(slot string, bold bool) lipgloss.Style { return m.st.Fg(slot).Bold(bold) }
	head := icons(win, plain) +
		m.st.Text().Bold(true).Render(fmt.Sprintf("%d %s", win.Index, win.Name)) +
		m.st.Muted().Render(" · "+pluralPanes(win.Panes)+" · active pane: "+win.Command)
	return append([]string{head}, split.Preview(m.preview, w, h-1)...)
}

func (m Model) header() string {
	extra := fmt.Sprintf(" · %d windows", len(m.rows))
	if len(m.rows) == 1 {
		extra = " · 1 window"
	}
	if n := len(m.undos); n > 0 {
		extra += fmt.Sprintf(" · %d to undo", n)
	}
	return m.st.Accent().Render(m.session) + m.st.Muted().Render(extra)
}

const hints = "j/k move · gg/G · ⏎ jump · space grab · r rename · x kill · u undo · q close"

func (m Model) footer() string {
	msg := func() string {
		switch m.msgKind {
		case msgErr:
			return m.st.Error().Render(m.msg)
		case msgOK:
			return m.st.Fg("state_done").Render(m.msg)
		}
		return m.st.Muted().Render(m.msg)
	}
	switch {
	case m.grab != "":
		f := m.st.Accent().Render("moving: j/k · space drop · esc cancel")
		if m.msg != "" {
			f = msg() + m.st.Muted().Render(" · ") + f
		}
		return f
	case m.mode == modeKill:
		w, _ := m.sel()
		return m.st.Error().Render(fmt.Sprintf(" kill window %d %s? (%s)", w.Index, w.Name, pluralPanes(w.Panes))) +
			m.st.Muted().Render("  y kill · other key cancels")
	case m.mode == modeRename:
		f := m.st.Accent().Render("rename: ⏎ save · C-u clear · esc cancel")
		if m.msg != "" {
			f = msg() + m.st.Muted().Render(" · ") + f
		}
		return f
	case m.msg != "":
		return msg()
	}
	return m.st.Muted().Render(hints)
}
