package style

import (
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// Palette maps every colorscheme slot (colorscheme.Slots) to "#rrggbb".
type Palette map[string]string

// PaletteOf is a scheme's palette.
func PaletteOf(s colorscheme.Scheme) Palette {
	p := Palette{}
	for _, slot := range colorscheme.Slots {
		p[slot] = s.Colors[slot]
	}
	return p
}

// LoadPalette reads the palette the server shows now: the 16
// @herdmux_c_<slot> options, in one tmux call. A slot that is unset (init
// has not run) takes flexoki-dark's colour.
func LoadPalette(c *tmux.Client) (Palette, error) {
	refs := make([]string, len(colorscheme.Slots))
	for i, slot := range colorscheme.Slots {
		refs[i] = colorscheme.Ref(slot)
	}
	out, err := c.Display("", tmux.Fields(refs...))
	if err != nil {
		return nil, err
	}
	p := PaletteOf(colorscheme.DefaultScheme())
	for i, v := range tmux.Split(out) {
		if i < len(colorscheme.Slots) && v != "" {
			p[colorscheme.Slots[i]] = v
		}
	}
	return p, nil
}

// Styles is the popup style family over one palette: the popup's bg, the
// selected row on bg_raised with an accent bar, hints in fg_muted, errors in
// attention_needs_input, borders in border. Output is always truecolor:
// inside tmux (TERM=tmux-256color, no COLORTERM) lipgloss would otherwise
// downsample every hex colour to 256.
//
// By default text styles set only a foreground, so unpainted cells show the
// popup's own background (popup-style, which the colorscheme sets to bg).
// Set Opaque to paint bg everywhere instead, for a popup that restyles itself
// live (tmux fixes popup-style when the popup opens).
type Styles struct {
	P      Palette
	Opaque bool
	r      *lipgloss.Renderer
}

// New returns the family for p.
func New(p Palette) *Styles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	return &Styles{P: p, r: r}
}

// Color is a slot's colour.
func (s *Styles) Color(slot string) lipgloss.Color { return lipgloss.Color(s.P[slot]) }

// Style is an empty style on the family's renderer.
func (s *Styles) Style() lipgloss.Style {
	st := s.r.NewStyle()
	if s.Opaque {
		st = st.Background(s.Color("bg"))
	}
	return st
}

// Fg is text in a slot's colour on the popup background.
func (s *Styles) Fg(slot string) lipgloss.Style { return s.Style().Foreground(s.Color(slot)) }

// On is text in fg on an explicit background slot.
func (s *Styles) On(fg, bg string) lipgloss.Style {
	return s.r.NewStyle().Foreground(s.Color(fg)).Background(s.Color(bg))
}

// Text is normal text (fg).
func (s *Styles) Text() lipgloss.Style { return s.Fg("fg") }

// Muted is hint text (fg_muted).
func (s *Styles) Muted() lipgloss.Style { return s.Fg("fg_muted") }

// Accent is the accent colour, bold: titles, prompts, highlighted keys.
func (s *Styles) Accent() lipgloss.Style { return s.Fg("accent").Bold(true) }

// Error is red text (attention_needs_input): errors and destructive confirms.
func (s *Styles) Error() lipgloss.Style { return s.Fg("attention_needs_input").Bold(true) }

// Selected is the selected row's text: fg, bold, on bg_raised.
func (s *Styles) Selected() lipgloss.Style { return s.On("fg", "bg_raised").Bold(true) }

// Pad truncates (with "…") or pads str to exactly w cells. Padding is
// painted bgSlot, or left unpainted when bgSlot is "" (and not Opaque).
func (s *Styles) Pad(str string, w int, bgSlot string) string {
	if w <= 0 {
		return ""
	}
	str = ansi.Truncate(str, w, "…")
	if d := w - ansi.StringWidth(str); d > 0 {
		fill := s.Style()
		if bgSlot != "" {
			fill = fill.Background(s.Color(bgSlot))
		}
		str += fill.Render(strings.Repeat(" ", d))
	}
	return str
}

// Bar is the selection bar that starts every list row: an accent "▌" on
// bg_raised when selected, a blank cell otherwise.
func (s *Styles) Bar(selected bool) string {
	if selected {
		return s.On("accent", "bg_raised").Render("▌")
	}
	return s.Style().Render(" ")
}

// Row is one list row w cells wide: the selection bar, then content padded
// to the width. A selected row's padding is bg_raised; content must paint
// its own background (use Selected or On(…, "bg_raised")) to match.
func (s *Styles) Row(selected bool, content string, w int) string {
	bg := ""
	if selected {
		bg = "bg_raised"
	}
	return s.Bar(selected) + s.Pad(content, w-1, bg)
}

// Frame draws a rounded border w×h cells around body, with title in accent
// on the top edge (`╭─ title ───╮`). Body lines are padded or cut to w-2
// cells, and missing lines are blank. Popups open with display-popup -B and
// draw their own frame, so the border follows the colorscheme (and restyles
// live with Opaque).
func (s *Styles) Frame(title string, w, h int, body []string) string {
	if w < 4 || h < 2 {
		return ""
	}
	bs := s.Fg("border")
	top := bs.Render("╭─")
	if title != "" {
		top += s.Fg("accent").Render(" " + title + " ")
	}
	top += bs.Render(strings.Repeat("─", max(0, w-1-ansi.StringWidth(top))) + "╮")
	top = ansi.Truncate(top, w, "")
	out := make([]string, 0, h)
	out = append(out, top)
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, bs.Render("│")+s.Pad(line, w-2, "")+bs.Render("│"))
	}
	out = append(out, bs.Render("╰"+strings.Repeat("─", w-2)+"╯"))
	return strings.Join(out, "\n")
}

// Wrap splits plain text into lines of at most w cells, at spaces where it
// can.
func Wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	return strings.Split(ansi.Wrap(text, w, ""), "\n")
}
