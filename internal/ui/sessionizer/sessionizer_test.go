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

func entries() []sessionizer.Entry {
	return []sessionizer.Entry{
		{Type: "session", Name: "api", Path: "/p/api", Session: "$1", Agents: 2},
		{Type: "session", Name: "main", Path: "/p", Session: "$0"},
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
	if m = press(m, "j", "enter"); m.Result().Switch == nil || m.Result().Switch.Name != "main" {
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
	m := press(newModel(Options{}), "/", "h", "e", "r", "d", "enter")
	uitest.Golden(t, *update, "filter", m.View())
	m = press(m, "enter")
	if r := m.Result().Switch; r == nil || r.Path != "/p/12.34-herdmux" {
		t.Errorf("result %+v", r)
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
