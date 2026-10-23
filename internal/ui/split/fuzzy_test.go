package split

import (
	"slices"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestFuzzy(t *testing.T) {
	t.Parallel()
	pos, _, ok := Fuzzy("hmx", "12.34-HerdMux")
	if !ok || !slices.Equal(pos, []int{6, 10, 12}) {
		t.Errorf("hmx on 12.34-HerdMux: %v %v", pos, ok)
	}
	if _, _, ok := Fuzzy("xh", "herdmux"); ok {
		t.Error("xh matched herdmux (order ignored)")
	}
	if pos, score, ok := Fuzzy("", "x"); !ok || pos != nil || score != 0 {
		t.Errorf("empty pattern: %v %d %v", pos, score, ok)
	}
	score := func(text string) int {
		_, s, ok := Fuzzy("web", text)
		if !ok {
			t.Fatalf("web did not match %q", text)
		}
		return s
	}
	if score("web") != score("my-web") || score("web") <= score("w-e-b") || score("web") <= score("wareb") {
		t.Errorf("ranking: web=%d my-web=%d w-e-b=%d wareb=%d", score("web"), score("my-web"), score("w-e-b"), score("wareb"))
	}
}

func TestHighlight(t *testing.T) {
	t.Parallel()
	base, hl := lipgloss.NewStyle(), lipgloss.NewStyle().Bold(true)
	got := Highlight("herdmux", []int{0, 4, 6}, base, hl)
	if want := hl.Render("h") + "erd" + hl.Render("m") + "u" + hl.Render("x"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := Highlight("abc", nil, base, hl); got != "abc" {
		t.Errorf("no positions: %q", got)
	}
}
