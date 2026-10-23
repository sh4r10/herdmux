package split

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Fuzzy matches pattern as a case-insensitive subsequence of text, the way
// fzf's v1 matcher does: the first window that holds every pattern rune,
// tightened from its end. pos is the matched rune index of each pattern
// rune (nil for an empty pattern, which matches everything) and score
// ranks matches, fzf-like: a match at the start of a word scores high and a
// run of adjacent matches keeps that bonus, while every gap costs. Higher
// is better.
func Fuzzy(pattern, text string) (pos []int, score int, ok bool) {
	p := lower(pattern)
	if len(p) == 0 {
		return nil, 0, true
	}
	t := lower(text)
	end, pi := -1, 0
	for i, r := range t {
		if r == p[pi] {
			if pi++; pi == len(p) {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return nil, 0, false
	}
	pos = make([]int, len(p))
	for i, pi := end, len(p)-1; pi >= 0; i-- {
		if t[i] == p[pi] {
			pos[pi] = i
			pi--
		}
	}
	prev := 0
	for k, i := range pos {
		bonus := 0
		if i == 0 || !(unicode.IsLetter(t[i-1]) || unicode.IsDigit(t[i-1])) {
			bonus = 8 // the start of a word
		}
		if k > 0 {
			if pos[k-1] == i-1 {
				bonus = max(bonus, prev, 4) // a run of matches keeps its start's bonus
			} else {
				score -= 3 + (i - pos[k-1] - 2) // a gap: 3, plus 1 per cell past the first
			}
		}
		score += bonus
		prev = bonus
	}
	return pos, score, true
}

// lower folds s rune by rune, so indexes into it are indexes into s (a
// string-wide ToLower can change the rune count).
func lower(s string) []rune {
	r := []rune(s)
	for i, c := range r {
		r[i] = unicode.ToLower(c)
	}
	return r
}

// Highlight renders text in base with the runes at pos (as Fuzzy returns
// them) in hl.
func Highlight(text string, pos []int, base, hl lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(text)
	}
	hit := make(map[int]bool, len(pos))
	for _, i := range pos {
		hit[i] = true
	}
	var out strings.Builder
	var run []rune
	flush := func(h bool) {
		if len(run) == 0 {
			return
		}
		st := base
		if h {
			st = hl
		}
		out.WriteString(st.Render(string(run)))
		run = run[:0]
	}
	cur := false
	for i, r := range []rune(text) {
		if hit[i] != cur {
			flush(cur)
			cur = hit[i]
		}
		run = append(run, r)
	}
	flush(cur)
	return out.String()
}
