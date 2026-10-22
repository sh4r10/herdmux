package colorscheme

// Slots are the 16 colour slots every v1 colorscheme defines, in a
// fixed order.
var Slots = []string{
	"pane_bg", "bg", "bg_raised", "bg_overlay",
	"fg", "fg_muted", "fg_on_attention",
	"border", "border_active",
	"accent",
	"state_working", "state_needs_input", "state_done", "state_unknown",
	"attention_needs_input", "attention_done",
}

// Option is the global tmux option holding a slot's colour, e.g.
// "@herdmux_c_bg_raised". Everything herdmux draws reads these, so applying a
// scheme means setting them; nothing is re-rendered.
func Option(slot string) string { return "@herdmux_c_" + slot }

// Ref is the tmux format reference to a slot's colour, "#{@herdmux_c_<slot>}".
func Ref(slot string) string { return "#{" + Option(slot) + "}" }
