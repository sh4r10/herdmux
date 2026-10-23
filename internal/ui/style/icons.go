package style

import "github.com/sh4r10/herdmux/internal/state"

// Nerd Font icons shared by the tabs (internal/statusbar) and the popups.
// Each is wider than its cell, so whatever follows one must start with a
// space (the tabs put one space after every icon).
const (
	IconWorking    = "" // nf-fa-circle, filled
	IconNeedsInput = "" // nf-fa-question_circle
	IconDone       = "" // nf-fa-check_circle
	IconUnknown    = "" // nf-fa-question
	IconZoom       = "" // nf-fa-expand: the window is zoomed
)

// StateIcon returns the icon for an agent state; any state other than
// working, needs-input or done gets the unknown icon.
func StateIcon(st string) string {
	switch st {
	case state.Working:
		return IconWorking
	case state.NeedsInput:
		return IconNeedsInput
	case state.Done:
		return IconDone
	}
	return IconUnknown
}

// StateSlot returns the colorscheme slot that colours a state's icon.
func StateSlot(st string) string {
	switch st {
	case state.Working:
		return "state_working"
	case state.NeedsInput:
		return "state_needs_input"
	case state.Done:
		return "state_done"
	}
	return "state_unknown"
}
