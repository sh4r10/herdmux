package state

import (
	"fmt"
	"strings"

	"herdmux/internal/tmux"
)

// Visit clears Attention on every pane of window (a window id, @N), whatever
// the agents' states. It runs from the pane-focus-in hook, which fires only
// when an attached client really shows the window.
func Visit(c *tmux.Client, window string) error {
	if !strings.HasPrefix(window, "@") {
		return fmt.Errorf("invalid window %q (want @N)", window)
	}
	out, err := c.Run("list-panes", "-t", window, "-F", tmux.Fields("#{pane_id}", "#{"+OptAttention+"}"))
	if err != nil {
		return err
	}
	var w writer
	for _, line := range tmux.Lines(out) {
		if f := tmux.Split(line); len(f) == 2 && f[1] != "" {
			w.unset(f[0], OptAttention)
		}
	}
	return w.flush(c)
}

// FocusHookIndex is the pane-focus-in array slot herdmux owns. A fixed high
// index leaves the user's own hooks (slot 0, `set-hook -a`) alone, and
// re-setting it makes Init idempotent.
const FocusHookIndex = 77

// FocusHookCommand is what the hook runs: tmux expands #{window_id} to the
// window gaining focus.
const FocusHookCommand = `run-shell -b "herdmux tmux-hook pane-focus-in #{window_id}"`

// Init turns the state layer on (this package's part of `herdmux init`):
// focus-events on (pane-focus-in needs it), the global HERDMUX=1 that gates
// the agent integrations, and the Visit hook. It is idempotent. The init verb
// runs Reconcile afterwards.
func Init(c *tmux.Client) error {
	_, err := c.Run(
		"set-option", "-g", "focus-events", "on", ";",
		"set-environment", "-g", "HERDMUX", "1", ";",
		"set-hook", "-g", fmt.Sprintf("pane-focus-in[%d]", FocusHookIndex), FocusHookCommand,
	)
	return err
}
