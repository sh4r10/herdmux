package sessionizer

import (
	"errors"
	"flag"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/sessionizer"
	"github.com/sh4r10/herdmux/internal/ui/style"
	"github.com/sh4r10/herdmux/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

func styles() *style.Styles { return style.New(style.PaletteOf(colorscheme.DefaultScheme())) }

// fake is the Backend over a fixed entry list.
type fake struct{ entries []sessionizer.Entry }

func (f fake) Entries() ([]sessionizer.Entry, error) { return f.entries, nil }
func (f fake) Capture(pane string, n int) []string {
	return []string{"$ echo hi from " + pane, "hi from " + pane}
}

func entries() []sessionizer.Entry {
	return []sessionizer.Entry{
		{Type: "session", Name: "api", Path: "/p/api", Session: "$1", Agents: 2, Pane: "%3"},
		{Type: "session", Name: "main", Path: "/p", Session: "$0", Pane: "%0"},
		{Type: "dir", Name: "12.34-herdmux", Path: "/p/12.34-herdmux"},
		{Type: "dir", Name: "web", Path: "/p/web"},
	}
}

func newModel(o Options) Model {
	o.Backend = fake{entries()}
	o.Styles = styles()
	mm, _ := New(o).Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	return mm.(Model)
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		mm, _ := m.Update(uitest.Key(k))
		m = mm.(Model)
	}
	return m
}

func TestDefaultView(t *testing.T) {
	t.Parallel()
	uitest.Golden(t, *update, "default", newModel(Options{}).View())
}

// A broken config.toml is shown in red above the footer, as in the picker
// and the launch prompt; the list still works.
func TestConfigError(t *testing.T) {
	t.Parallel()
	_, cfgErr := config.Parse([]byte("[sessionizer]\nroots = [\"projects\", \"/p\"]\n"))
	if cfgErr == nil {
		t.Fatal("want a config error")
	}
	st := styles()
	m := newModel(Options{ConfigErr: cfgErr})
	v := m.View()
	uitest.Golden(t, *update, "config-error", v)
	want := `config.toml: [sessionizer] roots: "projects" is not an absolute path`
	if !strings.Contains(ansi.Strip(v), want) {
		t.Errorf("view lacks %q:\n%s", want, ansi.Strip(v))
	}
	if !strings.Contains(v, "\x1b[1;"+uitest.FgSGR(st, "attention_needs_input")+"config.toml: ") {
		t.Error("the config error is not drawn in the error colour")
	}
	if m = press(m, "ctrl+n", "enter"); m.Result().Switch == nil || m.Result().Switch.Name != "main" {
		t.Errorf("result %+v, want main", m.Result().Switch)
	}
}

func TestNoConfigErrorNoLine(t *testing.T) {
	t.Parallel()
	if v := ansi.Strip(newModel(Options{}).View()); strings.Contains(v, "config.toml") {
		t.Errorf("unexpected config line:\n%s", v)
	}
}

func TestFilterAndEnter(t *testing.T) {
	t.Parallel()
	// The popup opens typing: no / needed, and "hmx" fuzzy-matches herdmux
	// (the matched runes are highlighted in accent).
	st := styles()
	m := press(newModel(Options{}), "h", "m", "x")
	uitest.Golden(t, *update, "filter", m.View())
	if rows := m.Rows(); len(rows) != 1 || rows[0].Name != "12.34-herdmux" {
		t.Fatalf("rows %+v", rows)
	}
	if v := m.View(); !strings.Contains(v, uitest.FgSGR(st, "accent")) || !strings.Contains(ansi.Strip(v), "/p/12.34-herdmux") {
		t.Errorf("no accent highlight or no full path in the view:\n%s", ansi.Strip(v))
	}
	m = press(m, "enter")
	if r := m.Result().Switch; r == nil || r.Path != "/p/12.34-herdmux" {
		t.Errorf("result %+v", r)
	}
}

// Ctrl-N/Ctrl-P move while typing, Ctrl-Y picks like Enter, and the first
// Esc clears the filter (the second closes).
func TestCtrlKeys(t *testing.T) {
	t.Parallel()
	m := press(newModel(Options{}), "ctrl+n", "ctrl+n", "ctrl+p")
	if m.Cursor() != 1 {
		t.Fatalf("cursor %d, want 1", m.Cursor())
	}
	m = press(m, "a", "p", "esc")
	if m.filter.Text != "" || len(m.Rows()) != 4 {
		t.Fatalf("esc did not clear the filter: %q, %d rows", m.filter.Text, len(m.Rows()))
	}
	if m = press(m, "ctrl+y"); m.Result().Switch == nil || m.Result().Switch.Name != "api" {
		t.Errorf("result %+v, want api", m.Result().Switch)
	}
	if m = press(newModel(Options{}), "esc"); !m.quit {
		t.Error("esc with an empty filter did not close")
	}
}

// Best matches first: typing "web" ranks the dir web above anything that
// merely contains the letters, and a session preview shows on the right.
func TestRankingAndPreview(t *testing.T) {
	t.Parallel()
	m := press(newModel(Options{}), "ma")
	if rows := m.Rows(); len(rows) == 0 || rows[0].Name != "main" {
		t.Fatalf("rows %+v, want main first", rows)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "hi from %0") || !strings.Contains(v, "⏎ switches to it") {
		t.Errorf("no preview of main:\n%s", v)
	}
}

func TestLoadError(t *testing.T) {
	t.Parallel()
	m := New(Options{Backend: errBackend{}, Styles: styles()})
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	if v := ansi.Strip(mm.(Model).View()); !strings.Contains(v, "can't list: boom") {
		t.Errorf("view:\n%s", v)
	}
}

type errBackend struct{}

func (errBackend) Entries() ([]sessionizer.Entry, error) { return nil, errors.New("boom") }
func (errBackend) Capture(string, int) []string          { return nil }
