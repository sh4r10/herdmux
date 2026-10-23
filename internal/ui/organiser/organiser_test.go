package organiser

import (
	"flag"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/style"
	"github.com/sh4r10/herdmux/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

func styles() *style.Styles { return style.New(style.PaletteOf(colorscheme.DefaultScheme())) }

// fake is the Backend over windows held in slot order (index = position+1,
// as with base-index 1 and renumber-windows on). Swap re-selects the
// current window by id, as tmux does after the fix-up.
type fake struct {
	wins     []Window
	captured []string
	swaps    [][4]string // a, b, current, last
	renames  []string
	unnamed  []string // "id name auto"
	killed   []string
	auto     map[string]string // window-local automatic-rename before a rename
}

func (f *fake) Windows() ([]Window, error) {
	out := slices.Clone(f.wins)
	for i := range out {
		out[i].Index = i + 1
	}
	return out, nil
}

func (f *fake) Capture(pane string, n int) []string {
	f.captured = append(f.captured, pane)
	return []string{"\x1b[32m$\x1b[0m echo " + pane, "output of " + pane}
}

func (f *fake) at(id string) int {
	return slices.IndexFunc(f.wins, func(w Window) bool { return w.ID == id })
}

func (f *fake) Swap(a, b, current, last string) error {
	f.swaps = append(f.swaps, [4]string{a, b, current, last})
	i, j := f.at(a), f.at(b)
	f.wins[i], f.wins[j] = f.wins[j], f.wins[i]
	return nil
}

func (f *fake) Rename(id, name string) (string, error) {
	f.renames = append(f.renames, id+" "+name)
	f.wins[f.at(id)].Name = name
	return f.auto[id], nil
}

func (f *fake) Unrename(id, name, auto string) error {
	f.unnamed = append(f.unnamed, strings.TrimSpace(id+" "+name+" "+auto))
	f.wins[f.at(id)].Name = name
	return nil
}

func (f *fake) Kill(id string) error {
	f.killed = append(f.killed, id)
	i := f.at(id)
	wasActive := f.wins[i].Active
	f.wins = slices.Delete(f.wins, i, i+1)
	if wasActive {
		f.wins[0].Active = true
	}
	return nil
}

func win(id, name string, panes int, cmd string, states []string, att string) Window {
	return Window{ID: id, Name: name, Panes: panes, Pane: "%" + id[1:], Command: cmd, States: states, Attention: att}
}

// windows of session "api"; the client is on window 3 (nvim), window 2 is
// the last window.
func windows() []Window {
	ws := []Window{
		win("@1", "shell", 1, "bash", nil, ""),
		win("@2", "claude", 1, "claude", []string{state.NeedsInput}, state.NeedsInput),
		win("@3", "nvim", 2, "nvim", nil, ""),
		win("@4", "agents", 3, "opencode", []string{state.Done, state.Working, state.NeedsInput}, state.NeedsInput),
		win("@5", "review", 1, "pi", []string{state.Done}, state.Done),
	}
	ws[2].Active = true
	ws[1].Last = true
	return ws
}

func newModel(f *fake) Model {
	return sized(New(Options{Backend: f, Styles: styles(), Session: "api"}), 120, 16)
}

func sized(m Model, w, h int) Model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(Model)
}

func pressMsg(m Model, msgs ...tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range msgs {
		var mm tea.Model
		mm, cmd = m.Update(k)
		m = mm.(Model)
	}
	return m, cmd
}

func press(m Model, keys ...string) (Model, tea.Cmd) {
	var msgs []tea.KeyMsg
	for _, k := range keys {
		msgs = append(msgs, uitest.Key(k))
	}
	return pressMsg(m, msgs...)
}

func order(m Model) string { return strings.Join(m.ids(), " ") }

func selID(m Model) string { return m.Rows()[m.Cursor()].ID }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func view(m Model) string { return ansi.Strip(m.View()) }

func TestOpensOnCurrentWindow(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	if m.Cursor() != 2 || m.curID != "@3" || m.lastID != "@2" {
		t.Errorf("cursor %d, current %s, last %s", m.Cursor(), m.curID, m.lastID)
	}
	if got := f.captured[len(f.captured)-1]; got != "%3" {
		t.Errorf("captured %s", got)
	}
}

func TestCursorMovesAndGG(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "j", "down", "j") // clamped at the bottom
	if m.Cursor() != 4 {
		t.Errorf("down: %d", m.Cursor())
	}
	if got := f.captured[len(f.captured)-1]; got != "%5" {
		t.Errorf("a move doesn't capture at once: %s", got)
	}
	m, _ = press(m, "k", "up")
	if m.Cursor() != 2 {
		t.Errorf("up: %d", m.Cursor())
	}
	m, _ = press(m, "g", "g")
	if m.Cursor() != 0 {
		t.Errorf("gg: %d", m.Cursor())
	}
	m, _ = press(m, "k")
	if m.Cursor() != 0 {
		t.Errorf("clamped at the top: %d", m.Cursor())
	}
	m, _ = press(m, "G")
	if m.Cursor() != 4 {
		t.Errorf("G: %d", m.Cursor())
	}
	m, _ = press(m, "g", "k") // a lone g does nothing; the next key still counts
	if m.Cursor() != 3 {
		t.Errorf("g then k: %d", m.Cursor())
	}
}

func TestGrabCarryDrop(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "k", " ") // grab @2 (claude)
	if m.Grabbed() != "@2" {
		t.Fatalf("grabbed %q", m.Grabbed())
	}
	m, _ = press(m, "j", "j")
	if got := order(m); got != "@1 @3 @4 @2 @5" {
		t.Errorf("carried: %s", got)
	}
	if selID(m) != "@2" || m.Cursor() != 3 {
		t.Errorf("cursor didn't follow the grabbed window: %d %s", m.Cursor(), selID(m))
	}
	// Each step is a live swap that keeps the current and last windows.
	want := [][4]string{{"@2", "@3", "@3", "@2"}, {"@2", "@4", "@3", "@2"}}
	if !slices.Equal(f.swaps, want) {
		t.Errorf("swaps %v, want %v", f.swaps, want)
	}
	if !strings.Contains(view(m), "moving: j/k · space drop · esc cancel") {
		t.Errorf("footer: %s", view(m))
	}
	m, _ = press(m, " ")
	if m.Grabbed() != "" || len(m.undos) != 1 {
		t.Errorf("drop: grabbed %q, undos %d", m.Grabbed(), len(m.undos))
	}
	// ⏎ drops too; a drop that moved nothing leaves no undo.
	m, _ = press(m, " ", "enter")
	if m.Grabbed() != "" || len(m.undos) != 1 {
		t.Errorf("enter drop: grabbed %q, undos %d", m.Grabbed(), len(m.undos))
	}
	if strings.Contains(view(m), "moving:") || !strings.Contains(view(m), "1 to undo") {
		t.Errorf("after drop: %s", view(m))
	}
	// Carrying past the ends does nothing.
	m, _ = press(m, "G", " ", "j")
	if len(f.swaps) != 2 {
		t.Errorf("carried past the bottom: %v", f.swaps)
	}
}

func TestGrabEscRestores(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "g", "g", " ", "j", "j", "j")
	if got := order(m); got != "@2 @3 @4 @1 @5" {
		t.Fatalf("carried: %s", got)
	}
	m, cmd := press(m, "esc")
	if isQuit(cmd) || m.Grabbed() != "" {
		t.Fatalf("esc closed or still grabbed")
	}
	if got := order(m); got != "@1 @2 @3 @4 @5" {
		t.Errorf("esc: %s", got)
	}
	if len(m.undos) != 0 {
		t.Errorf("an esc'd grab is undoable: %v", m.undos)
	}
	if selID(m) != "@1" {
		t.Errorf("selection %s", selID(m))
	}
}

func TestGGIgnoredWhileGrabbed(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, " ", "g", "g", "G", "q", "x", "r", "u")
	if m.Cursor() != 2 || m.Grabbed() != "@3" || len(f.swaps) != 0 || m.mode != modeList {
		t.Errorf("cursor %d grabbed %q swaps %v mode %v", m.Cursor(), m.Grabbed(), f.swaps, m.mode)
	}
	m, _ = press(m, "j") // still carrying: gg left no pending g behind
	if got := order(m); got != "@1 @2 @4 @3 @5" {
		t.Errorf("after j: %s", got)
	}
}

func TestUndoDropsAndRenames(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows(), auto: map[string]string{"@1": "on"}}
	m := newModel(f)
	m, _ = press(m, " ", "k", " ") // nvim to 2
	m, _ = press(m, "G", " ", "k", "k", " ")
	if got := order(m); got != "@1 @3 @5 @2 @4" {
		t.Fatalf("after two drops: %s", got)
	}
	m, _ = press(m, "g", "g", "r")
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = press(m, "s", "h", "enter")
	if m.Rows()[0].Name != "sh" || !slices.Equal(f.renames, []string{"@1 sh"}) {
		t.Fatalf("rename: %v", f.renames)
	}
	if !strings.Contains(view(m), "3 to undo") {
		t.Errorf("header: %s", view(m))
	}
	m, _ = press(m, "u") // the rename
	if !slices.Equal(f.unnamed, []string{"@1 shell on"}) || m.Rows()[0].Name != "shell" {
		t.Errorf("undo rename: %v", f.unnamed)
	}
	if !strings.Contains(view(m), "undid rename") {
		t.Errorf("message: %s", view(m))
	}
	m, _ = press(m, "u") // the second drop
	if got := order(m); got != "@1 @3 @2 @4 @5" {
		t.Errorf("undo drop 2: %s", got)
	}
	m, _ = press(m, "u") // the first
	if got := order(m); got != "@1 @2 @3 @4 @5" {
		t.Errorf("undo drop 1: %s", got)
	}
	for _, s := range f.swaps {
		if s[2] != "@3" || s[3] != "@2" {
			t.Errorf("a swap didn't keep current/last: %v", s)
		}
	}
	m, _ = press(m, "u")
	if !strings.Contains(view(m), "nothing to undo") {
		t.Errorf("empty stack: %s", view(m))
	}
}

func TestKillIsNotUndoneAndUndoSkipsIt(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "g", "g", " ", "G", "j", "j", "j", "j", " ") // shell to the end
	if got := order(m); got != "@2 @3 @4 @5 @1" {
		t.Fatalf("carried: %s", got)
	}
	m, _ = press(m, "g", "g", "x", "y") // kill @2
	if !slices.Equal(f.killed, []string{"@2"}) {
		t.Fatalf("killed %v", f.killed)
	}
	m, _ = press(m, "u")
	if got := order(m); got != "@1 @3 @4 @5" {
		t.Errorf("undo after a kill: %s", got)
	}
	if len(m.undos) != 0 {
		t.Errorf("the kill went on the stack: %v", m.undos)
	}
}

func TestRename(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "r")
	if string(m.edit) != "nvim" || !strings.Contains(view(m), "nvim█") {
		t.Errorf("prefill %q: %s", string(m.edit), view(m))
	}
	m, _ = press(m, "backspace", "backspace", " ", "x", "q") // q is text here
	if string(m.edit) != "nv xq" {
		t.Errorf("edit %q", string(m.edit))
	}
	m, _ = press(m, "esc")
	if m.mode != modeList || len(f.renames) != 0 || m.Rows()[2].Name != "nvim" {
		t.Errorf("esc: mode %v renames %v", m.mode, f.renames)
	}
	// C-u clears; an empty name is refused and the field stays.
	m, _ = press(m, "r")
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = press(m, " ", "enter")
	if m.mode != modeRename || len(f.renames) != 0 || !strings.Contains(view(m), "name can't be empty") {
		t.Errorf("empty name: mode %v renames %v: %s", m.mode, f.renames, view(m))
	}
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, cmd := press(m, "e", "d", "enter")
	if isQuit(cmd) || m.mode != modeList || !slices.Equal(f.renames, []string{"@3 ed"}) || m.Rows()[2].Name != "ed" {
		t.Errorf("save: mode %v renames %v", m.mode, f.renames)
	}
	// Saving the same name changes nothing and leaves no undo.
	m, _ = press(m, "r", "enter")
	if len(f.renames) != 1 || len(m.undos) != 1 {
		t.Errorf("same name: %v, %d undos", f.renames, len(m.undos))
	}
}

func TestKillConfirmAndLastWindow(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	m, _ = press(m, "j", "x")
	if !strings.Contains(view(m), " kill window 4 agents? (3 panes)") {
		t.Errorf("confirm: %s", view(m))
	}
	m, _ = press(m, "n") // any other key cancels
	if len(f.killed) != 0 || m.mode != modeList {
		t.Fatalf("cancel: %v", f.killed)
	}
	m, _ = press(m, "x", "y")
	if !slices.Equal(f.killed, []string{"@4"}) {
		t.Fatalf("killed %v", f.killed)
	}
	if m.Cursor() != 3 || selID(m) != "@5" || !strings.Contains(view(m), "killed window 4 agents") {
		t.Errorf("after kill: cursor %d on %s: %s", m.Cursor(), selID(m), view(m))
	}
	// Killing the current window: tmux picks the next, and ▸ follows.
	m, _ = press(m, "k", "x", "y")
	if m.curID != "@1" {
		t.Errorf("current after killing it: %s", m.curID)
	}
	// The session's last window can't be killed.
	f2 := &fake{wins: windows()[:1]}
	f2.wins[0].Active = true
	m = newModel(f2)
	m, _ = press(m, "x")
	if m.mode != modeList || !strings.Contains(view(m), "can't kill the session's last window") {
		t.Errorf("last window: mode %v: %s", m.mode, view(m))
	}
	m, _ = press(m, "y")
	if len(f2.killed) != 0 {
		t.Errorf("killed the last window")
	}
}

func TestJumpAndClose(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{wins: windows()})
	m, cmd := press(m, "j", "enter")
	if !isQuit(cmd) || m.Result().Jump != "@4" || m.View() != "" {
		t.Errorf("enter: quit %v result %+v", isQuit(cmd), m.Result())
	}
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		if _, cmd := press(newModel(&fake{wins: windows()}), k); !isQuit(cmd) {
			t.Errorf("%s doesn't close", k)
		}
	}
	// The opening key (prefix r: r without the prefix) renames, it doesn't close.
	if m, cmd := press(newModel(&fake{wins: windows()}), "r"); isQuit(cmd) || m.mode != modeRename {
		t.Error("r closed")
	}
}

func TestTickRefreshes(t *testing.T) {
	t.Parallel()
	f := &fake{wins: windows()}
	m := newModel(f)
	n := len(f.captured)
	f.wins = append([]Window{win("@9", "new", 1, "sh", nil, "")}, f.wins...)
	mm, cmd := m.Update(split.Tick{})
	m = mm.(Model)
	if cmd == nil || len(f.captured) != n+1 {
		t.Errorf("tick: next %v, captures %d→%d", cmd != nil, n, len(f.captured))
	}
	if selID(m) != "@3" {
		t.Errorf("selection didn't follow its window: %s", selID(m))
	}
}

func TestRowColours(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{wins: windows()})
	st := m.st
	v := m.View()
	// @4 has done, working and needs-input agents: needs-input wins, and
	// all three icons and the name are bold in its colour.
	att := "\x1b[1;" + uitest.FgSGR(st, "attention_needs_input")
	want := att + style.IconDone + " \x1b[0m" + att + style.IconWorking + " \x1b[0m" + att + style.IconNeedsInput + " \x1b[0m" + att + "agents"
	if !strings.Contains(v, want) {
		t.Errorf("attention row: %q", v)
	}
	// The selected row paints bg_raised; the ▸ mark is muted.
	if !strings.Contains(v, uitest.BgSGR(st, "bg_raised")) {
		t.Error("no selected background")
	}
	m, _ = press(m, "j") // off the current window: its ▸ is muted on no background
	if v := m.View(); !strings.Contains(v, uitest.FgSGR(st, "fg_muted")+"▸") {
		t.Errorf("no muted ▸: %q", v)
	}
}

func TestGolden(t *testing.T) {
	t.Parallel()
	ctrlU := tea.KeyMsg{Type: tea.KeyCtrlU}
	cases := map[string]func(Model) Model{
		"default": func(m Model) Model { return m },
		// The cursor on the Attention rows: claude (needs-input), review (done).
		"attention": func(m Model) Model { m, _ = press(m, "k"); return m },
		"grabbed":   func(m Model) Model { m, _ = press(m, " ", "j"); return m },
		"rename": func(m Model) Model {
			m, _ = press(m, "r")
			m, _ = pressMsg(m, ctrlU)
			m, _ = press(m, "e", "d", "i", "t", "o", "r")
			return m
		},
		"kill-confirm": func(m Model) Model { m, _ = press(m, "j", "x"); return m },
	}
	for name, do := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uitest.Golden(t, *update, name, do(newModel(&fake{wins: windows()})).View())
		})
	}
}
