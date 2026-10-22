package split

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"herdmux/internal/ui/style"
)

// Filter is the popups' `/` filter: `/` starts
// typing (Start), printable keys and space append, backspace deletes, ⏎
// keeps the filter and stops typing, Esc clears it. While not typing, a
// kept filter stays applied; the popup decides what Esc does then (the
// pickers clear a kept filter on the first Esc, then close).
type Filter struct {
	Text   string
	Typing bool
}

// Start begins typing (the `/` key).
func (f *Filter) Start() { f.Typing = true }

// Clear drops the filter and stops typing.
func (f *Filter) Clear() { *f = Filter{} }

// Key handles k while typing and reports whether it used it; when it
// didn't (not typing, or a key such as an arrow), the popup handles k.
// changed is true when Text changed (re-filter the list).
func (f *Filter) Key(k tea.KeyMsg) (used, changed bool) {
	if !f.Typing {
		return false, false
	}
	switch k.Type {
	case tea.KeyEnter:
		f.Typing = false
		return true, false
	case tea.KeyEsc:
		had := f.Text != ""
		f.Clear()
		return true, had
	case tea.KeyBackspace:
		if f.Text == "" {
			return true, false
		}
		r := []rune(f.Text)
		f.Text = string(r[:len(r)-1])
		return true, true
	case tea.KeySpace:
		f.Text += " "
		return true, true
	case tea.KeyRunes:
		if k.Alt {
			return false, false
		}
		f.Text += string(k.Runes)
		return true, true
	}
	return false, false
}

// Active reports whether the filter shows in the header.
func (f Filter) Active() bool { return f.Typing || f.Text != "" }

// Prompt is the header's filter field: "> text", with a block cursor while
// typing ("" when inactive).
func (f Filter) Prompt(st *style.Styles) string {
	if !f.Active() {
		return ""
	}
	cursor := ""
	if f.Typing {
		cursor = "█"
	}
	return st.Text().Render("> " + f.Text + cursor)
}

// Match is the filter test: a case-insensitive substring of any field
// joined by spaces ("" matches everything).
func Match(text string, fields ...string) bool {
	if text == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.Join(fields, " ")), strings.ToLower(text))
}
