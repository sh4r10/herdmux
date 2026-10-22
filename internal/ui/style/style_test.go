package style

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/colorscheme"
	"herdmux/internal/tmuxtest"
)

func flexoki() *Styles { return New(PaletteOf(colorscheme.DefaultScheme())) }

func TestFrame(t *testing.T) {
	t.Parallel()
	st := flexoki()
	out := st.Frame("title", 20, 5, []string{"one", strings.Repeat("x", 40)})
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("line %d is %d wide: %q", i, w, ansi.Strip(l))
		}
	}
	want := []string{
		"╭─ title ──────────╮",
		"│one               │",
		"│xxxxxxxxxxxxxxxxx…│",
		"│                  │",
		"╰──────────────────╯",
	}
	if got := ansi.Strip(out); got != strings.Join(want, "\n") {
		t.Errorf("frame:\n%s", got)
	}
	// The border is drawn in the border slot, the title in accent; truecolor.
	if !strings.Contains(out, "\x1b[38;2;64;62;60m╭─") || !strings.Contains(out, "\x1b[38;2;206;93;151m title ") {
		t.Errorf("colours: %q", out)
	}
}

func TestRowAndOpaque(t *testing.T) {
	t.Parallel()
	st := flexoki()
	sel := st.Row(true, st.Selected().Render("agent"), 12)
	if ansi.StringWidth(sel) != 12 || ansi.Strip(sel) != "▌agent      " {
		t.Errorf("selected row %q", ansi.Strip(sel))
	}
	if !strings.Contains(sel, "48;2;64;62;60m") { // bg_raised #403e3c
		t.Errorf("selected row has no bg_raised: %q", sel)
	}
	plain := st.Row(false, st.Text().Render("agent"), 12)
	if strings.Contains(plain, "[48;") {
		t.Errorf("unselected row paints a background: %q", plain)
	}
	st.Opaque = true
	if got := st.Text().Render("x"); !strings.Contains(got, "48;2;28;27;26m") { // bg #1c1b1a
		t.Errorf("opaque text has no bg: %q", got)
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()
	got := Wrap("config.toml: agent x: missing command", 16)
	for _, l := range got {
		if ansi.StringWidth(l) > 16 {
			t.Errorf("line %q too wide", l)
		}
	}
	if strings.Join(got, " ") != "config.toml: agent x: missing command" {
		t.Errorf("wrap %q", got)
	}
}

func TestLoadPalette(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	p, err := LoadPalette(s.Client)
	if err != nil {
		t.Fatal(err)
	}
	if p["accent"] != colorscheme.DefaultScheme().Colors["accent"] {
		t.Errorf("before any apply: accent %q, want flexoki-dark's", p["accent"])
	}
	g, _ := colorscheme.BundledScheme("gruvbox-dark")
	if err := colorscheme.Apply(s.Client, g); err != nil {
		t.Fatal(err)
	}
	p, err = LoadPalette(s.Client)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range colorscheme.Slots {
		if p[slot] != g.Colors[slot] {
			t.Errorf("%s = %q, want %q", slot, p[slot], g.Colors[slot])
		}
	}
}
