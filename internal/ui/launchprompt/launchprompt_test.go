package launchprompt

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

func styles() *style.Styles { return style.New(style.PaletteOf(colorscheme.DefaultScheme())) }

// golden compares got with testdata/<name>.golden (raw ANSI: `cat` it to
// see the popup) and its plain-text twin <name>.txt.
func golden(t *testing.T, name, got string) {
	t.Helper()
	for ext, want := range map[string]string{".golden": got, ".txt": ansi.Strip(got)} {
		p := filepath.Join("testdata", name+ext)
		if *update {
			if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if string(b) != want {
			t.Errorf("%s differs; got:\n%s\nwant:\n%s", p, ansi.Strip(got), ansi.Strip(string(b)))
		}
	}
}

// fg is the truecolor escape that starts text in slot's colour.
func fg(slot string) string {
	var r, g, b int
	_, _ = fmt.Sscanf(styles().P[slot], "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("38;2;%d;%d;%dm", r, g, b)
}

func key(k string) tea.KeyMsg {
	switch k {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func opus() []config.Agent {
	cfg, err := config.Parse([]byte(`
[[agent]]
name = "claude"
command = "claude"
[[agent]]
name = "claude-opus"
command = "claude --model opus"
[[agent]]
name = "pi"
command = "pi"
`))
	if err != nil {
		panic(err)
	}
	return cfg.Agents
}

func TestViewDefaults(t *testing.T) {
	t.Parallel()
	m := New(Prompt{Agents: config.DefaultAgents(), Where: "~/src/api"}, styles())
	v := m.View()
	golden(t, "defaults", v)
	w, h := m.P.Size()
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		t.Errorf("%d lines, Size says %d", len(lines), h)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) != w {
			t.Errorf("line %d is %d wide, want %d", i, ansi.StringWidth(l), w)
		}
	}
	// The keys are drawn in accent (flexoki-dark #ce5d97), bold.
	if !strings.Contains(v, fg("accent")+"c") {
		t.Errorf("key c is not in accent: %q", v)
	}
}

func TestViewAutoPickedKeyAndCommand(t *testing.T) {
	t.Parallel()
	m := New(Prompt{Agents: opus(), Where: "/tmp"}, styles())
	golden(t, "auto-pick", m.View())
	if !strings.Contains(m.View(), fg("accent")+"l") {
		t.Error("claude-opus's auto-picked l is not highlighted")
	}
}

func TestViewConfigError(t *testing.T) {
	t.Parallel()
	_, err := config.Parse([]byte("[[agent]]\nname = \"x\"\n"))
	m := New(Prompt{Agents: config.DefaultAgents(), Err: err, Where: "~"}, styles())
	v := m.View()
	golden(t, "config-error", v)
	if !strings.Contains(v, fg("attention_needs_input")+"config.toml:") {
		t.Errorf("no red config.toml line in %q", v)
	}
	if !strings.Contains(ansi.Strip(v), `agent "x": missing command`) {
		t.Error("error text missing")
	}
}

func TestViewNoAgents(t *testing.T) {
	t.Parallel()
	golden(t, "no-agents", New(Prompt{Where: "~"}, styles()).View())
}

func TestUpdateKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		keys   []string
		chosen string // "" = none
		quit   bool
	}{
		{[]string{"l"}, "claude-opus", true},
		{[]string{"c"}, "claude", true},
		{[]string{"x", "z", "p"}, "pi", true}, // other keys are ignored
		{[]string{"q"}, "", true},
		{[]string{"esc"}, "", true},
		{[]string{"ctrl+c"}, "", true},
		{[]string{"x"}, "", false},
	} {
		var m tea.Model = New(Prompt{Agents: opus()}, styles())
		var cmd tea.Cmd
		for _, k := range tc.keys {
			m, cmd = m.Update(key(k))
		}
		a, ok := m.(Model).Chosen()
		if (tc.chosen != "") != ok || a.Name != tc.chosen {
			t.Errorf("%v: chosen %q %v, want %q", tc.keys, a.Name, ok, tc.chosen)
		}
		quit := cmd != nil && cmd() == tea.Quit()
		if quit != tc.quit {
			t.Errorf("%v: quit %v", tc.keys, quit)
		}
	}
}

func TestResizeAndInline(t *testing.T) {
	t.Parallel()
	m, _ := New(Prompt{Agents: opus()}, styles()).Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 12 || ansi.StringWidth(lines[0]) != 50 {
		t.Errorf("view is %d×%d after a resize to 50×12", ansi.StringWidth(lines[0]), len(lines))
	}
	p := Prompt{Agents: opus()}
	if got := ansi.Strip(p.Inline(styles())); got != "launch: c claude · l claude-opus · p pi" {
		t.Errorf("inline %q", got)
	}
	if got := ansi.Strip(Prompt{}.Inline(styles())); got != "launch: no agents found on PATH" {
		t.Errorf("inline with no agents %q", got)
	}
}

func TestShortDir(t *testing.T) {
	t.Parallel()
	for dir, want := range map[string]string{"/home/u": "~", "/home/u/src": "~/src", "/home/user": "/home/user", "/tmp": "/tmp"} {
		if got := ShortDir(dir, "/home/u/"); got != want {
			t.Errorf("ShortDir(%q) = %q, want %q", dir, got, want)
		}
	}
}
