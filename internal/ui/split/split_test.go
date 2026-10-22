package split

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/colorscheme"
	"herdmux/internal/tmuxtest"
	"herdmux/internal/ui/style"
	"herdmux/internal/ui/uitest"
)

func TestLayoutRender(t *testing.T) {
	t.Parallel()
	st := style.New(style.PaletteOf(colorscheme.DefaultScheme()))
	out := Layout{
		Title:  "t",
		Header: "head",
		Left:   []string{"a", strings.Repeat("x", 30)},
		Right:  []string{"r1"},
		Above:  []string{"err"},
		Footer: "foot",
	}.Render(st, 40, 10)
	want := []string{
		"╭─ t ──────────────────────────────────╮",
		"│ head                                 │",
		"│                                      │",
		"│ a                │ r1                │",
		"│ xxxxxxxxxxxxxxx… │                   │",
		"│                  │                   │",
		"│                  │                   │",
		"│ err                                  │",
		"│ foot                                 │",
		"╰──────────────────────────────────────╯",
	}
	if got := ansi.Strip(out); got != strings.Join(want, "\n") {
		t.Errorf("layout:\n%s", got)
	}
	if l, r := Columns(200); l != MaxList || r != 200-MaxList-3 {
		t.Errorf("columns %d %d", l, r)
	}
}

func TestOffset(t *testing.T) {
	t.Parallel()
	for _, c := range [][3]int{{0, 5, 0}, {4, 5, 0}, {5, 5, 1}, {9, 5, 5}} {
		if got := Offset(c[0], c[1]); got != c[2] {
			t.Errorf("Offset(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()
	var f Filter
	if used, _ := f.Key(uitest.Key("j")); used {
		t.Fatal("used a key while not typing")
	}
	f.Start()
	for _, k := range []string{"a", " ", "b", "backspace"} {
		if used, changed := f.Key(uitest.Key(k)); !used || !changed {
			t.Errorf("%q: used=%v changed=%v", k, used, changed)
		}
	}
	if f.Text != "a " {
		t.Errorf("text %q", f.Text)
	}
	if used, _ := f.Key(uitest.Key("down")); used {
		t.Error("an arrow was used as text")
	}
	if used, changed := f.Key(uitest.Key("enter")); !used || changed || f.Typing || f.Text != "a " {
		t.Errorf("enter: %+v", f)
	}
	f.Start()
	if used, changed := f.Key(uitest.Key("esc")); !used || !changed || f.Active() {
		t.Errorf("esc: %+v", f)
	}
	if !Match("API", "claude", "api:1 x") || Match("zz", "a", "b") || !Match("", "x") || !Match("e a", "claude", "api") {
		t.Error("Match")
	}
}

func TestPreviewAndLastLines(t *testing.T) {
	t.Parallel()
	got := Preview([]string{"one", "\x1b[41mtwo-is-long\x1b[0m", "three"}, 5, 2)
	if len(got) != 2 || ansi.Strip(got[0]) != "two-i" || !strings.HasSuffix(got[0], "\x1b[0m") {
		t.Errorf("preview %q", got)
	}
	if got := lastLines("a\nb\nc\n\x1b[0m  \n\n", 2); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("lastLines %q", got)
	}
}

func TestCapture(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("send-keys", "-t", s.Pane, "-l", "for i in 1 2 3 4 5 6 7 8; do echo line$i; done")
	s.Tmux("send-keys", "-t", s.Pane, "Enter")
	var lines []string
	s.Eventually(3*time.Second, "the output", func() bool {
		var err error
		lines, err = Capture(s.Client, s.Pane, 3)
		return err == nil && len(lines) == 3 && strings.Contains(lines[1], "line8")
	})
	if !strings.Contains(lines[0], "line7") {
		t.Errorf("capture %q", lines)
	}
}
