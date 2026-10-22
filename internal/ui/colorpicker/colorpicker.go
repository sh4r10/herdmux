// Package colorpicker is the colorscheme picker popup: the split layout
// (internal/ui/split) with every scheme on the left, grouped by family, and
// the highlighted scheme's swatches and a mini mock of tmux on the right.
//
// Moving the highlight restyles the whole server live (one
// colorscheme.Apply per move). The popup paints every cell in the
// highlighted scheme's colours and draws its own border (it opens with
// display-popup -B, and tmux fixes popup-style when a popup opens), so it
// restyles itself too.
//
// Keys: j/k and the arrows move (invalid schemes are skipped), g/G go to the
// first/last scheme, ⏎ saves and closes, Esc/q revert to the saved scheme
// and close (the first Esc clears a kept filter), / filters by name or
// family.
//
// While it is open it polls the saved choice every split.Tick: a save made
// elsewhere (MCP set_colorscheme, `herdmux colorscheme <name>`) moves the ✓
// and the highlight, becomes the revert target and shows in the footer.
//
// Model talks to tmux only through a Backend (TmuxBackend in a popup, a fake
// in tests). The save or revert is returned by Result after the program
// quits; Finish carries it out.
package colorpicker

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/colorscheme"
	"herdmux/internal/tmux"
	"herdmux/internal/ui/split"
	"herdmux/internal/ui/style"
)

// Backend is what the model needs: the schemes and the saved choice (the
// store), and the live apply (tmux).
type Backend interface {
	List() ([]colorscheme.Scheme, error) // colorscheme.Store.List
	Saved() (string, error)              // colorscheme.Store.Saved
	Apply(s colorscheme.Scheme) error    // colorscheme.Apply: restyle the server
}

// TmuxBackend is the Backend on a real server and config dir.
type TmuxBackend struct {
	C     *tmux.Client
	Store *colorscheme.Store
}

func (b TmuxBackend) List() ([]colorscheme.Scheme, error) { return b.Store.List() }
func (b TmuxBackend) Saved() (string, error)              { return b.Store.Saved() }
func (b TmuxBackend) Apply(s colorscheme.Scheme) error    { return colorscheme.Apply(b.C, s) }

// Options configure New.
type Options struct {
	Backend Backend
	Active  string // the scheme the server shows now (colorscheme.Active)
}

// Result is what the user chose; Finish acts on it after the program quits.
// Without a Save (Esc, q, Ctrl-C, or the program failing) the server goes
// back to the saved scheme.
type Result struct {
	Save string // ⏎: choose this scheme (apply and save)
}

// Finish carries out res: Set the chosen scheme, or ApplySaved (the revert,
// to whatever is saved now, including a save made elsewhere while open).
func Finish(c *tmux.Client, st *colorscheme.Store, res Result) error {
	if res.Save != "" {
		_, err := colorscheme.Set(c, st, res.Save)
		return err
	}
	_, err := colorscheme.ApplySaved(c, st)
	return err
}

// item is one list line: a family header, or a scheme (an index into all).
type item struct {
	header string
	si     int // -1 for a header
}

// Model is the colorscheme picker.
type Model struct {
	b       Backend
	all     []colorscheme.Scheme
	items   []item
	cur     int // index into items; -1 when nothing is selectable
	saved   string
	applied string // the scheme the server shows (as far as the picker knows)
	pal     style.Palette
	filter  split.Filter
	msg     string
	msgErr  bool
	loadErr error

	w, h   int
	result Result
	quit   bool
}

// New loads the schemes and the saved choice and highlights the saved scheme
// (or, when it can't be selected, the one the server shows). Opening never
// restyles the server unless the highlighted scheme isn't the one shown.
func New(o Options) Model {
	m := Model{b: o.Backend, applied: o.Active, cur: -1}
	m.all, m.loadErr = m.b.List()
	m.saved, _ = m.b.Saved()
	m.pal = style.PaletteOf(colorscheme.DefaultScheme())
	if s, ok := m.find(o.Active); ok && s.Valid() {
		m.pal = style.PaletteOf(s)
	}
	m.build()
	if !m.jumpTo(m.saved) && !m.jumpTo(o.Active) {
		m.first()
	}
	m.preview()
	return m
}

// Result is the save chosen, if any.
func (m Model) Result() Result { return m.result }

// Saved is the revert target: the saved choice, as last seen.
func (m Model) Saved() string { return m.saved }

// Selected is the highlighted scheme's name ("" when none).
func (m Model) Selected() string {
	if s, ok := m.sel(); ok {
		return s.Name
	}
	return ""
}

// Groups are the family headers shown, in order (for tests).
func (m Model) Groups() []string {
	var out []string
	for _, it := range m.items {
		if it.si < 0 {
			out = append(out, it.header)
		}
	}
	return out
}

func (m Model) find(name string) (colorscheme.Scheme, bool) {
	i := slices.IndexFunc(m.all, func(s colorscheme.Scheme) bool { return s.Name == name })
	if i < 0 {
		return colorscheme.Scheme{}, false
	}
	return m.all[i], true
}

func (m Model) sel() (colorscheme.Scheme, bool) {
	if m.cur >= 0 && m.cur < len(m.items) && m.items[m.cur].si >= 0 {
		return m.all[m.items[m.cur].si], true
	}
	return colorscheme.Scheme{}, false
}

// selectable: a scheme row whose scheme is valid. Invalid schemes are shown
// but the cursor skips them, so they are never applied or saved.
func (m Model) selectable(i int) bool {
	return i >= 0 && i < len(m.items) && m.items[i].si >= 0 && m.all[m.items[i].si].Valid()
}

// build lays out the rows: the schemes matching the filter, by family
// (colorscheme.Families order, user-only last). The highlight stays on its
// scheme when it is still listed, else it goes to the first selectable row.
func (m *Model) build() {
	keep := m.Selected()
	m.items = nil
	for _, f := range colorscheme.Families {
		var rows []item
		for i, s := range m.all {
			if s.Family == f && split.Match(m.filter.Text, s.Name, s.Family) {
				rows = append(rows, item{si: i})
			}
		}
		if len(rows) > 0 {
			m.items = append(m.items, item{header: f, si: -1})
			m.items = append(m.items, rows...)
		}
	}
	m.cur = -1
	if keep == "" || !m.jumpTo(keep) {
		m.first()
	}
}

// jumpTo highlights name if it is listed and selectable.
func (m *Model) jumpTo(name string) bool {
	for i, it := range m.items {
		if it.si >= 0 && m.all[it.si].Name == name && m.selectable(i) {
			m.cur = i
			return true
		}
	}
	return false
}

func (m *Model) first() {
	m.cur = -1
	m.step(0, 1)
}

func (m *Model) last() {
	m.cur = len(m.items)
	m.step(len(m.items)-1, -1)
	if m.cur == len(m.items) {
		m.cur = -1
	}
}

// step moves to the first selectable row from i in direction d; with none,
// the cursor stays where it is.
func (m *Model) step(i, d int) {
	for ; i >= 0 && i < len(m.items); i += d {
		if m.selectable(i) {
			m.cur = i
			return
		}
	}
}

// preview restyles the server (and the popup) to the highlighted scheme: one
// tmux call, only when it changes what the server shows.
func (m *Model) preview() {
	s, ok := m.sel()
	if !ok {
		return
	}
	m.pal = style.PaletteOf(s)
	if s.Name == m.applied {
		return
	}
	if err := m.b.Apply(s); err != nil {
		m.msg, m.msgErr = "can't apply "+s.Name+": "+err.Error(), true
		return
	}
	m.applied = s.Name
}

func (m Model) Init() tea.Cmd { return split.Every() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case split.Tick:
		m.follow()
		return m, split.Every()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// follow picks up a save made elsewhere: the ✓ moves, the highlight jumps to
// it (clearing a filter that hides it), Esc now reverts to it, and the
// footer says so. The list is re-read, since the save may follow a new
// user scheme.
func (m *Model) follow() {
	saved, err := m.b.Saved()
	if err != nil || saved == m.saved {
		return
	}
	m.saved = saved
	if all, err := m.b.List(); err == nil {
		m.all, m.loadErr = all, nil
	}
	m.build()
	if !m.jumpTo(saved) && m.filter.Active() {
		m.filter.Clear()
		m.build()
	}
	m.jumpTo(saved)
	m.preview()
	m.msg, m.msgErr = "saved elsewhere (MCP): "+saved+" · esc now reverts to it", false
}

func (m Model) close(res Result) (Model, tea.Cmd) {
	m.result, m.quit = res, true
	return m, tea.Quit
}

func (m Model) key(msg tea.KeyMsg) (Model, tea.Cmd) {
	if used, changed := m.filter.Key(msg); used {
		m.msg = ""
		if changed {
			m.build()
			m.preview()
		}
		return m, nil
	}
	m.msg = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return m.close(Result{})
	case "esc":
		if m.filter.Text != "" { // the first Esc clears a kept filter
			m.filter.Clear()
			m.build()
			m.preview()
			return m, nil
		}
		return m.close(Result{})
	case "enter":
		if s, ok := m.sel(); ok {
			return m.close(Result{Save: s.Name})
		}
		return m, nil
	case "j", "down":
		m.step(m.cur+1, 1)
	case "k", "up":
		if m.cur > 0 {
			m.step(m.cur-1, -1)
		}
	case "g", "home":
		m.first()
	case "G", "end":
		m.last()
	case "/":
		m.filter.Start()
	}
	m.preview()
	return m, nil
}

// ---- view: every cell is painted in the highlighted scheme's colours.

func (m Model) styles() *style.Styles {
	st := style.New(m.pal)
	st.Opaque = true
	return st
}

func (m Model) View() string {
	if m.w == 0 || m.quit {
		return "" // not sized yet, or closing
	}
	st := m.styles()
	inner := split.Inner(m.w)
	lw, rw := split.Columns(inner)
	bh := split.BodyHeight(m.h, 0)
	return split.Layout{
		Title:  "colorscheme",
		Header: m.header(st),
		Left:   m.list(st, lw, bh),
		Right:  m.panel(st, rw, bh),
		Footer: m.footer(st),
	}.Render(st, m.w, m.h)
}

func tag(s colorscheme.Scheme) string {
	switch s.Source {
	case colorscheme.Override:
		return "override"
	case colorscheme.User:
		return "user"
	}
	return ""
}

// row is `▌✓ name  tag`. An invalid scheme is dimmed and struck through,
// with a red ! and its short reason. Only the selected row has a background
// (bg_raised, with the accent bar).
func (m Model) row(st *style.Styles, s colorscheme.Scheme, w int, selected bool) string {
	seg := func(slot string) lipgloss.Style {
		if selected {
			return st.On(slot, "bg_raised")
		}
		return st.Fg(slot)
	}
	mark := seg("fg").Render("  ")
	switch {
	case !s.Valid():
		mark = seg("attention_needs_input").Bold(true).Render("! ")
	case s.Name == m.saved:
		mark = seg("accent").Bold(true).Render("✓ ")
	}
	var content string
	if !s.Valid() {
		content = mark + seg("fg_muted").Strikethrough(true).Render(s.Name) +
			seg("attention_needs_input").Render("  "+s.Reason())
	} else {
		content = mark + seg("fg").Bold(selected).Render(s.Name)
	}
	if t := tag(s); t != "" {
		content += seg("fg_muted").Render("  " + t)
	}
	return st.Row(selected, content, w)
}

func (m Model) list(st *style.Styles, w, h int) []string {
	if len(m.items) == 0 {
		msg := "no colorschemes match"
		if m.loadErr != nil {
			msg = "can't list colorschemes: " + m.loadErr.Error()
		}
		return []string{st.Muted().Render("  " + msg)}
	}
	var out []string
	for i := split.Offset(max(0, m.cur), h); i < len(m.items) && len(out) < h; i++ {
		it := m.items[i]
		if it.si < 0 {
			out = append(out, st.Muted().Bold(true).Render(" "+it.header))
			continue
		}
		out = append(out, m.row(st, m.all[it.si], w, i == m.cur))
	}
	return out
}

// where is the scheme's origin: bundled, user/<name>.toml, or the override.
func where(s colorscheme.Scheme) string {
	switch s.Source {
	case colorscheme.Override:
		return "user/" + s.Name + ".toml, replaces the bundled " + s.Name
	case colorscheme.User:
		return "user/" + s.Name + ".toml"
	}
	return "bundled"
}

// swatchW is one labelled swatch: a 4-cell block, the longest slot name
// (attention_needs_input) and the hex.
const swatchW = 4 + 1 + 21 + 1 + 7 + 2

// panel is the highlighted scheme: its name and dark or light, its origin, all
// 16 slots as labelled swatches with hex, then the mini mock.
func (m Model) panel(st *style.Styles, w, h int) []string {
	s, ok := m.sel()
	if !ok {
		return nil
	}
	out := []string{
		st.Text().Bold(true).Render(s.Name) + st.Muted().Render(" · "+s.Tone()),
		st.Muted().Render(where(s)),
		"",
	}
	cols := 1
	if w >= 2*swatchW {
		cols = 2
	}
	for i := 0; i < len(colorscheme.Slots); i += cols {
		line := ""
		for j := i; j < i+cols && j < len(colorscheme.Slots); j++ {
			slot := colorscheme.Slots[j]
			cell := st.On(slot, slot).Render("    ") + st.Text().Render(" "+slot+" ") + st.Muted().Render(s.Colors[slot])
			line += st.Pad(cell, swatchW, "")
		}
		out = append(out, line)
	}
	out = append(out, "", st.Muted().Render("preview"))
	out = append(out, mock(st, w)...)
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// mock is a tiny tmux in the scheme's colours: two panes split by the active
// border (a prompt, a done line, a muted comment; a menu row and a copy
// selection), a message, and the status bar with a current tab, a
// needs-input and a done tab and an unknown one.
func mock(st *style.Styles, w int) []string {
	on := st.On
	fill := func(s, bg string) string { return st.Pad(s, w, bg) }
	half := max(10, (w-1)/2)
	pane := func(l, r string) string {
		return fill(st.Pad(l, half, "pane_bg")+on("border_active", "pane_bg").Render("│")+r, "pane_bg")
	}
	tab := func(fg, bg, icon string, iconFg string, label string, bold bool) string {
		return on(fg, bg).Bold(bold).Render(" ") + on(iconFg, bg).Render(icon+" ") + on(fg, bg).Bold(bold).Render(label+" ")
	}
	bar := on("accent", "bg").Bold(true).Render(" herdmux ") +
		tab("fg", "bg_raised", style.IconWorking, "state_working", "1:shell", true) +
		tab("fg_on_attention", "attention_needs_input", style.IconNeedsInput, "fg_on_attention", "2:claude", false) +
		tab("fg_on_attention", "attention_done", style.IconDone, "fg_on_attention", "3:opencode", false) +
		tab("fg_muted", "bg", style.IconUnknown, "state_unknown", "4:notes", false)
	right := on("fg_muted", "bg").Render("~/herdmux ") + on("fg", "bg").Render(" main") + on("state_working", "bg").Render(" ● ")
	if d := w - ansi.StringWidth(bar) - ansi.StringWidth(right); d > 0 {
		bar += on("fg", "bg").Render(strings.Repeat(" ", d))
	}
	return []string{
		pane(on("fg", "pane_bg").Render(" $ go test ./..."),
			on("fg", "pane_bg").Render(" ")+on("fg", "bg_overlay").Render(" menu: split  ")+on("fg", "bg_raised").Bold(true).Render(" zoom ")),
		pane(on("state_done", "pane_bg").Render(" ok  herdmux 0.42s"),
			on("fg", "pane_bg").Render(" ")+on("fg_on_attention", "accent").Render("copy selection")+on("fg", "pane_bg").Render(" more text")),
		pane(on("fg_muted", "pane_bg").Render(" # comment, muted"), ""),
		fill(on("border_active", "pane_bg").Render(strings.Repeat("─", half)+"┴")+on("border", "pane_bg").Render(strings.Repeat("─", max(0, w-half-1))), "pane_bg"),
		fill(on("fg", "bg_overlay").Render(" message: colorscheme saved "), "pane_bg"),
		st.Pad(bar+right, w, "bg"),
	}
}

func (m Model) header(st *style.Styles) string {
	n := 0
	for _, it := range m.items {
		if it.si >= 0 {
			n++
		}
	}
	h := st.Accent().Render(fmt.Sprintf("%d schemes", n)) + st.Muted().Render(" · saved "+m.saved)
	if p := m.filter.Prompt(st); p != "" {
		h = p + st.Style().Render("   ") + h
	}
	return h
}

const hints = "j/k move · g/G first/last · ⏎ save · esc revert · / filter · q close"

func (m Model) footer(st *style.Styles) string {
	switch {
	case m.msg != "" && m.msgErr:
		return st.Error().Render(m.msg)
	case m.msg != "":
		return st.Fg("state_done").Render(m.msg)
	case m.filter.Typing:
		return st.Muted().Render("type to filter · ↑/↓ move · ⏎ keep · esc clear")
	case m.filter.Text != "":
		return st.Muted().Render("esc clears the filter · " + hints)
	}
	return st.Muted().Render(hints)
}
