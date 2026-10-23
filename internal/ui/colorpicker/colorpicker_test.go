package colorpicker

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// schemeText is a valid scheme body (from a bundled scheme) with edits: a ""
// value drops the slot.
func schemeText(from string, edits map[string]string) string {
	g, _ := colorscheme.BundledScheme(from)
	var b strings.Builder
	b.WriteString("[colors]\n")
	for _, slot := range colorscheme.Slots {
		v := g.Colors[slot]
		if e, ok := edits[slot]; ok {
			v = e
		}
		if v != "" {
			b.WriteString(slot + ` = "` + v + "\"\n")
		}
	}
	return b.String()
}

// store is a config dir with a new user scheme, an
// override of a bundled one, and an invalid user scheme.
func store(t *testing.T) *colorscheme.Store {
	t.Helper()
	st := colorscheme.NewStore(t.TempDir())
	files := map[string]string{
		"midnight-moss":    schemeText("kanagawa-dragon", nil),
		"tokyonight-night": schemeText("tokyonight-night", map[string]string{"pane_bg": "#000000", "accent": "#ff79c6"}),
		"broken-neon":      schemeText("gruvbox-dark", map[string]string{"border": "#12345", "attention_done": ""}),
	}
	if err := os.MkdirAll(st.ColorsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, body := range files {
		if err := os.WriteFile(st.UserPath(n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func save(t *testing.T, st *colorscheme.Store, name string) {
	t.Helper()
	if err := os.WriteFile(st.SavedPath(), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fake is the Backend over a real store, recording applies.
type fake struct {
	st      *colorscheme.Store
	applied []string
}

func (f *fake) List() ([]colorscheme.Scheme, error) { return f.st.List() }
func (f *fake) Saved() (string, error)              { return f.st.Saved() }
func (f *fake) Apply(s colorscheme.Scheme) error {
	f.applied = append(f.applied, s.Name)
	return nil
}

func newModel(t *testing.T, f *fake) Model {
	t.Helper()
	saved, _ := f.st.Saved()
	return sized(New(Options{Backend: f, Active: saved}), 150, 44)
}

func sized(m Model, w, h int) Model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(Model)
}

func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var mm tea.Model
		mm, cmd = m.Update(uitest.Key(k))
		m = mm.(Model)
	}
	return m, cmd
}

func tick(m Model) Model {
	mm, _ := m.Update(split.Tick{})
	return mm.(Model)
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestGroupingOrder(t *testing.T) {
	t.Parallel()
	m := newModel(t, &fake{st: store(t)})
	want := []string{"flexoki", "tokyonight", "catppuccin", "kanagawa", "rose-pine", "gruvbox", "onedark", "user"}
	if got := m.Groups(); !slices.Equal(got, want) {
		t.Errorf("groups %v", got)
	}
	// The override sits in its bundled family; user-only schemes are last, by name.
	var names []string
	for _, it := range m.items {
		if it.si >= 0 {
			names = append(names, m.all[it.si].Name)
		}
	}
	if names[2] != "tokyonight-night" || !slices.Equal(names[len(names)-2:], []string{"broken-neon", "midnight-moss"}) {
		t.Errorf("names %v", names)
	}
}

func TestOpenHighlightsSavedWithoutApplying(t *testing.T) {
	t.Parallel()
	st := store(t)
	save(t, st, "gruvbox-dark")
	f := &fake{st: st}
	m := newModel(t, f)
	if m.Selected() != "gruvbox-dark" || len(f.applied) != 0 {
		t.Errorf("selected %q, applied %v", m.Selected(), f.applied)
	}
	// An invalid saved scheme can't be highlighted: the one the server shows is.
	save(t, st, "broken-neon")
	f = &fake{st: st}
	m = sized(New(Options{Backend: f, Active: "flexoki-dark"}), 150, 44)
	if m.Selected() != "flexoki-dark" || len(f.applied) != 0 {
		t.Errorf("invalid saved: selected %q, applied %v", m.Selected(), f.applied)
	}
}

func TestMoveAppliesOncePerMove(t *testing.T) {
	t.Parallel()
	f := &fake{st: store(t)}
	m := newModel(t, f)
	m, _ = press(m, "j", "down", "k")
	if want := []string{"flexoki-light", "tokyonight-night", "flexoki-light"}; !slices.Equal(f.applied, want) {
		t.Errorf("applied %v", f.applied)
	}
	// A key that doesn't move (k at the top) applies nothing.
	m, _ = press(m, "k", "k")
	if m.Selected() != "flexoki-dark" || len(f.applied) != 4 {
		t.Errorf("at top: %q %v", m.Selected(), f.applied)
	}
}

func TestCursorSkipsInvalid(t *testing.T) {
	t.Parallel()
	f := &fake{st: store(t)}
	m := newModel(t, f)
	m, _ = press(m, "G")
	if m.Selected() != "midnight-moss" {
		t.Fatalf("G: %q", m.Selected())
	}
	m, _ = press(m, "k") // skips broken-neon and the user header
	if m.Selected() != "onedark-light" {
		t.Errorf("k over the invalid row: %q", m.Selected())
	}
	m, _ = press(m, "j")
	if m.Selected() != "midnight-moss" {
		t.Errorf("j over the invalid row: %q", m.Selected())
	}
	m, _ = press(m, "g")
	if m.Selected() != "flexoki-dark" {
		t.Errorf("g: %q", m.Selected())
	}
	if slices.Contains(f.applied, "broken-neon") {
		t.Error("the invalid scheme was applied")
	}

	// Filter edges: only the invalid scheme matches, so nothing is selected,
	// nothing is applied, and ⏎ saves nothing.
	m, _ = press(m, "/", "b", "r", "o", "k")
	if m.Selected() != "" || slices.Contains(f.applied, "broken-neon") {
		t.Errorf("only invalid listed: %q %v", m.Selected(), f.applied)
	}
	m, _ = press(m, "enter", "g", "G", "j")
	if m.Selected() != "" {
		t.Errorf("selected %q", m.Selected())
	}
	if m, cmd := press(m, "enter"); isQuit(cmd) || m.Result().Save != "" {
		t.Error("⏎ with nothing selectable")
	}
	// The invalid row first in a filtered list: the cursor starts below it.
	m, _ = press(m, "esc", "/", "u", "s", "e", "r")
	if s := m.Selected(); s != "midnight-moss" {
		t.Errorf("filter /user: %q", s)
	}
}

func TestFilterKeepAndClear(t *testing.T) {
	t.Parallel()
	f := &fake{st: store(t)}
	m := newModel(t, f)
	m, _ = press(m, "/", "c", "a", "t")
	if got := m.Groups(); !slices.Equal(got, []string{"catppuccin"}) || m.Selected() != "catppuccin-mocha" {
		t.Errorf("filtered: %v %q", got, m.Selected())
	}
	if f.applied[len(f.applied)-1] != "catppuccin-mocha" {
		t.Errorf("the filtered highlight wasn't applied: %v", f.applied)
	}
	m, _ = press(m, "down") // arrows move while typing
	if m.Selected() != "catppuccin-macchiato" {
		t.Errorf("down while typing: %q", m.Selected())
	}
	m, _ = press(m, "enter") // keeps the filter
	if m.filter.Typing || m.filter.Text != "cat" {
		t.Errorf("kept: %+v", m.filter)
	}
	if !strings.Contains(ansi.Strip(m.View()), "esc clears the filter") {
		t.Error("kept-filter hint")
	}
	m, cmd := press(m, "esc") // the first Esc clears the kept filter
	if isQuit(cmd) || m.filter.Active() || len(m.Groups()) != 8 || m.Selected() != "catppuccin-macchiato" {
		t.Errorf("first esc: quit=%v %+v %q", isQuit(cmd), m.filter, m.Selected())
	}
	m, cmd = press(m, "esc")
	if !isQuit(cmd) || m.Result().Save != "" {
		t.Error("second esc should close and revert")
	}
	// Family matches too, and Esc while typing clears.
	m = newModel(t, f)
	m, _ = press(m, "/", "u", "s", "e", "r")
	if got := m.Groups(); !slices.Equal(got, []string{"user"}) {
		t.Errorf("/user: %v", got)
	}
	m, _ = press(m, "esc")
	if m.filter.Active() || len(m.Groups()) != 8 {
		t.Errorf("esc while typing: %+v", m.filter)
	}
}

func TestEnterSavesAndCloses(t *testing.T) {
	t.Parallel()
	m := newModel(t, &fake{st: store(t)})
	m, cmd := press(m, "j", "enter")
	if !isQuit(cmd) || m.Result().Save != "flexoki-light" || m.View() != "" {
		t.Errorf("enter: quit=%v %+v", isQuit(cmd), m.Result())
	}
}

func TestEscAndQRevert(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"esc", "q", "ctrl+c"} {
		m := newModel(t, &fake{st: store(t)})
		m, cmd := press(m, "j", "j", k)
		if !isQuit(cmd) || m.Result() != (Result{}) {
			t.Errorf("%s: quit=%v %+v", k, isQuit(cmd), m.Result())
		}
	}
	// q is text while typing.
	m := newModel(t, &fake{st: store(t)})
	if _, cmd := press(m, "/", "q"); isQuit(cmd) {
		t.Error("q closed while typing")
	}
}

func TestSavedElsewhere(t *testing.T) {
	t.Parallel()
	st := store(t)
	f := &fake{st: st}
	m := newModel(t, f)
	m, _ = press(m, "/", "c", "a", "t", "enter") // a kept filter that hides the new choice
	m = tick(m)
	if m.msg != "" || m.Saved() != colorscheme.Default {
		t.Fatalf("no change: %q %q", m.msg, m.Saved())
	}
	save(t, st, "gruvbox-dark") // as MCP set_colorscheme does
	m = tick(m)
	if m.Saved() != "gruvbox-dark" || m.Selected() != "gruvbox-dark" || m.filter.Active() {
		t.Errorf("followed: saved %q selected %q filter %+v", m.Saved(), m.Selected(), m.filter)
	}
	if f.applied[len(f.applied)-1] != "gruvbox-dark" {
		t.Errorf("applied %v", f.applied)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "saved elsewhere (MCP): gruvbox-dark") || !strings.Contains(v, "✓ gruvbox-dark") || strings.Contains(v, "✓ flexoki-dark") {
		t.Errorf("view:\n%s", v)
	}
	uitest.Golden(t, *update, "saved-elsewhere", m.View())
	// Esc now reverts to it: the result is a revert, and Finish applies the saved file.
	m, _ = press(m, "j")
	if _, cmd := press(m, "esc"); !isQuit(cmd) {
		t.Error("esc")
	}
	// A save to a scheme the filter shows keeps the filter.
	m = newModel(t, f)
	m, _ = press(m, "/", "g", "r", "u", "v", "enter")
	save(t, st, "gruvbox-light")
	m = tick(m)
	if m.filter.Text != "gruv" || m.Selected() != "gruvbox-light" {
		t.Errorf("visible: %+v %q", m.filter, m.Selected())
	}
}

func TestViewGoldens(t *testing.T) {
	t.Parallel()
	st := store(t)
	f := &fake{st: st}
	m := newModel(t, f)
	uitest.Golden(t, *update, "default", m.View())

	// The painted palette is the highlighted scheme's, everywhere: frame,
	// gutter and padding all carry its bg.
	sty := m.styles()
	v := m.View()
	if !strings.Contains(v, "▌") || !strings.Contains(v, uitest.BgSGR(sty, "bg_raised")) {
		t.Error("selected row")
	}
	for _, line := range strings.Split(v, "\n") {
		if !strings.HasPrefix(line, "\x1b[") || strings.Contains(line, "\x1b[0m ") {
			t.Errorf("unpainted cells: %q", line)
			break
		}
	}

	// The invalid row: dimmed, struck through, red ! and the reason.
	invalid := sty.Fg("fg_muted").Strikethrough(true).Render("broken-neon")
	if !strings.Contains(v, invalid) || !strings.Contains(v, sty.Fg("attention_needs_input").Bold(true).Render("! ")) {
		t.Error("invalid row style")
	}
	if !strings.Contains(ansi.Strip(v), "! broken-neon  bad border, no attention_done  user") {
		t.Errorf("invalid row text:\n%s", ansi.Strip(v))
	}
	gm, _ := press(m, "G") // next to the invalid row, the user scheme's panel
	uitest.Golden(t, *update, "invalid", gm.View())
	if !strings.Contains(ansi.Strip(gm.View()), "user/midnight-moss.toml") {
		t.Error("user panel")
	}

	// The override's panel: swatches, the mock, and where it comes from, all
	// in the override's colours.
	pm, _ := press(m, "j", "j")
	if pm.Selected() != "tokyonight-night" {
		t.Fatalf("selected %q", pm.Selected())
	}
	pv := pm.View()
	uitest.Golden(t, *update, "panel", pv)
	plain := ansi.Strip(pv)
	for _, want := range []string{"replaces the bundled tokyonight-night", "pane_bg #000000", "accent #ff79c6", "copy selection", "2:claude", "override"} {
		if !strings.Contains(plain, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	if pst := pm.styles(); !strings.Contains(pv, uitest.BgSGR(pst, "pane_bg")) || uitest.BgSGR(pst, "pane_bg") == uitest.BgSGR(sty, "pane_bg") {
		t.Error("the popup isn't painted in the highlighted scheme")
	}

	fm, _ := press(m, "/", "r", "o", "s", "e")
	uitest.Golden(t, *update, "filter", fm.View())
	if !strings.Contains(ansi.Strip(fm.View()), "> rose█") {
		t.Error("filter prompt")
	}
}

// Against a real server: each move restyles it (one apply), and the revert
// puts the saved scheme back.
func TestTmuxMoveAndRevert(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	st := colorscheme.NewStore(filepath.Join(t.TempDir(), "config"))
	if _, err := colorscheme.ApplySaved(s.Client, st); err != nil {
		t.Fatal(err)
	}
	active := func() string { a, _ := colorscheme.Active(s.Client); return a }
	m := sized(New(Options{Backend: TmuxBackend{C: s.Client, Store: st}, Active: active()}), 150, 44)
	for _, want := range []string{"flexoki-light", "tokyonight-night", "tokyonight-storm"} {
		m, _ = press(m, "j")
		if got := active(); got != want {
			t.Fatalf("after j: %q, want %q", got, want)
		}
	}
	if got := s.Tmux("show-options", "-gv", "pane-active-border-style"); got != "fg="+mustGet(t, st, "tokyonight-storm").Colors["border_active"] {
		t.Errorf("styles not applied: %q", got)
	}
	m, cmd := press(m, "esc")
	if !isQuit(cmd) {
		t.Fatal("esc")
	}
	if err := Finish(s.Client, st, m.Result()); err != nil {
		t.Fatal(err)
	}
	if got := active(); got != colorscheme.Default {
		t.Errorf("after esc: %q", got)
	}
	if _, err := os.Stat(st.SavedPath()); err == nil {
		t.Error("the revert saved something")
	}
	// ⏎ applies and saves.
	if err := Finish(s.Client, st, Result{Save: "gruvbox-light"}); err != nil {
		t.Fatal(err)
	}
	if saved, _ := st.Saved(); saved != "gruvbox-light" || active() != "gruvbox-light" {
		t.Errorf("saved %q active %q", saved, active())
	}
}

func mustGet(t *testing.T, st *colorscheme.Store, name string) colorscheme.Scheme {
	t.Helper()
	s, err := st.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
