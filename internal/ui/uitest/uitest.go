// Package uitest holds test helpers for the popups' bubbletea models:
// golden View files and key messages. Import it from _test.go files only.
package uitest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"herdmux/internal/ui/style"
)

// Golden compares got with testdata/<name>.golden (raw ANSI: `cat` it to see
// the popup) and its plain-text twin <name>.txt. With update set (a
// package's `-update` flag) it rewrites both instead.
func Golden(t *testing.T, update bool, name, got string) {
	t.Helper()
	for ext, want := range map[string]string{".golden": got, ".txt": ansi.Strip(got)} {
		p := filepath.Join("testdata", name+ext)
		if update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
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

// Key is the tea.KeyMsg bubbletea delivers for k, named as KeyMsg.String()
// names it: "enter", "esc", "tab", "up", "down", "backspace", "ctrl+c",
// " " (or "space"), or printable text ("j", "/").
func Key(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "space", " ": // KeySpace's String() is " "
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// FgSGR is the SGR fragment ("38;2;r;g;bm") that starts text in slot's
// colour as st renders it, for asserting colours in a View. It comes from
// the renderer, not the hex: lipgloss may round a channel by one.
func FgSGR(st *style.Styles, slot string) string {
	return sgr(st.Fg(slot).Render("X"), "38;")
}

// BgSGR is the background fragment ("48;2;r;g;bm") for slot. Combined with
// a foreground it comes after it in one sequence ("38;…;48;2;r;g;bm").
func BgSGR(st *style.Styles, slot string) string {
	return sgr(st.On("fg", slot).Render("X"), "48;")
}

// sgr cuts the fragment starting at prefix out of the first "\x1b[…mX".
func sgr(rendered, prefix string) string {
	seq, _, _ := strings.Cut(strings.TrimPrefix(rendered, "\x1b["), "mX")
	if i := strings.Index(seq, prefix); i >= 0 {
		seq = seq[i:]
	}
	return seq + "m"
}
