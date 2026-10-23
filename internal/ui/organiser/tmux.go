package organiser

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/split"
)

// TmuxBackend is the Backend on a real server, for one session. Every
// target is qualified with the session id ("$1:@4"), because a window
// linked into several sessions would otherwise resolve to any of them.
type TmuxBackend struct {
	C       *tmux.Client
	Session string // the session id, $N
	// FocusEvents is the global focus-events value (NewTmuxBackend reads it); Swap
	// turns it off around its last-window fix-up when it is on.
	FocusEvents bool
}

// NewTmuxBackend is the backend for session (an id, $N), with the
// focus-events value read from the server.
func NewTmuxBackend(c *tmux.Client, session string) (TmuxBackend, error) {
	out, err := c.Run("show-options", "-gv", "focus-events")
	if err != nil {
		return TmuxBackend{}, err
	}
	return TmuxBackend{C: c, Session: session, FocusEvents: strings.TrimSpace(out) == "on"}, nil
}

func (b TmuxBackend) target(id string) string { return b.Session + ":" + id }

// window_name goes last, so a tab in a name can't shift the fields.
var windowsFormat = tmux.Fields("#{window_id}", "#{window_index}", "#{window_panes}", "#{pane_id}",
	"#{pane_active}", "#{pane_current_command}", "#{window_active}", "#{window_last_flag}",
	"#{"+state.OptAgent+"}", "#{"+state.OptState+"}", "#{"+state.OptAttention+"}", "#{window_name}")

// Windows lists the session's windows by index, from one list-panes: the
// active pane of each, and its agents' states and Attention (needs-input
// beats done), as the window tab aggregates them.
func (b TmuxBackend) Windows() ([]Window, error) {
	out, err := b.C.Run("list-panes", "-s", "-t", b.Session, "-F", windowsFormat)
	if err != nil {
		return nil, err
	}
	return parseWindows(out), nil
}

func parseWindows(out string) []Window {
	var ws []Window
	at := map[string]int{}
	for _, line := range tmux.Lines(out) {
		f := strings.SplitN(line, tmux.FieldSep, 12)
		if len(f) != 12 {
			continue
		}
		i, ok := at[f[0]]
		if !ok {
			idx, _ := strconv.Atoi(f[1])
			panes, _ := strconv.Atoi(f[2])
			i = len(ws)
			at[f[0]] = i
			ws = append(ws, Window{ID: f[0], Index: idx, Panes: panes, Name: f[11],
				Active: f[6] == "1", Last: f[7] == "1"})
		}
		w := &ws[i]
		if f[4] == "1" {
			w.Pane, w.Command = f[3], f[5]
		}
		if f[8] != "" {
			w.States = append(w.States, f[9])
			switch {
			case f[10] == state.NeedsInput:
				w.Attention = state.NeedsInput
			case f[10] != "" && w.Attention == "":
				w.Attention = state.Done
			}
		}
	}
	slices.SortStableFunc(ws, func(a, b Window) int { return a.Index - b.Index })
	return ws
}

func (b TmuxBackend) Capture(pane string, n int) []string {
	lines, _ := split.Capture(b.C, pane, n) // a pane that just went away shows nothing
	return lines
}

// Swap is one step of a reorder, in one tmux call:
//
//	set -g focus-events off ;
//	swap-window -d -s <a> -t <b> ;
//	select-window -t <last> ; select-window -t <current> ;
//	set -g focus-events on
//
// swap-window exchanges the windows held by two index slots, so indexes
// stay gapless. tmux's current window and its last-window stack (prefix l)
// are slots, not windows, so a swap moves them onto other windows, and
// current is re-selected by id.
//
// Checked during build (tmux 3.5a), and pinned by the tests:
//   - The re-select alone does disturb the last window. Selecting a window
//     pushes the slot that was current onto the last-window stack, so
//     prefix l would go to the neighbour; and a swap that moves the last
//     window's contents makes prefix l go to whatever lands in its slot.
//     So Swap selects the last window, then current: that leaves the last
//     window on top of the stack. (With no last window, only current.)
//   - swap-window -d makes the slot it swapped into current (in 3.5a -d
//     selects and its absence doesn't), so carrying a window that isn't
//     current shows it for a moment. Each select of another window fires
//     pane-focus-in on its pane, herdmux's Visit, which would clear its
//     Attention. So focus-events is off for the whole step and back on in
//     the same command list. Nothing redraws in between, and every pane
//     keeps the focus it had.
func (b TmuxBackend) Swap(a, bb, current, last string) error {
	var args []string
	add := func(cmd ...string) {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, cmd...)
	}
	if b.FocusEvents {
		add("set-option", "-g", "focus-events", "off")
	}
	add("swap-window", "-d", "-s", b.target(a), "-t", b.target(bb))
	if last != "" && last != current && current != "" {
		add("select-window", "-t", b.target(last))
	}
	if current != "" {
		add("select-window", "-t", b.target(current))
	}
	if b.FocusEvents {
		add("set-option", "-g", "focus-events", "on")
	}
	_, err := b.C.Run(args...)
	return err
}

// nameArg is a window name as rename-window takes it (it expands formats).
func nameArg(name string) string { return tmux.FormatLiteral(name) }

func (b TmuxBackend) Rename(id, name string) (string, error) {
	auto, err := b.C.Run("show-options", "-wv", "-t", b.target(id), "automatic-rename")
	if err != nil {
		return "", err
	}
	_, err = b.C.Run("rename-window", "-t", b.target(id), "--", nameArg(name))
	return strings.TrimSpace(auto), err
}

// Unrename restores the name, then automatic-rename: unset when the window
// had no local value (rename-window set it off), else that value.
func (b TmuxBackend) Unrename(id, name, auto string) error {
	args := []string{"rename-window", "-t", b.target(id), "--", nameArg(name), ";"}
	if auto == "" {
		args = append(args, "set-option", "-wu", "-t", b.target(id), "automatic-rename")
	} else {
		args = append(args, "set-option", "-w", "-t", b.target(id), "automatic-rename", auto)
	}
	_, err := b.C.Run(args...)
	return err
}

// Kill kills the window. With renumber-windows on, tmux closes the gap;
// if it was current, tmux picks the next current window.
func (b TmuxBackend) Kill(id string) error {
	_, err := b.C.Run("kill-window", "-t", b.target(id))
	return err
}

// Select makes the window the session's current one: the jump.
func Select(c *tmux.Client, session, id string) error {
	_, err := c.Run("select-window", "-t", session+":"+id)
	return err
}

// ClientSession is the id and name of the session client shows, from
// list-clients (`display -c` would expand in the caller's pane).
func ClientSession(c *tmux.Client, client string) (id, name string, err error) {
	out, err := c.Run("list-clients", "-F", tmux.Fields("#{client_name}", "#{session_id}", "#{session_name}"))
	if err != nil {
		return "", "", err
	}
	for _, line := range tmux.Lines(out) {
		if f := strings.SplitN(line, tmux.FieldSep, 3); len(f) == 3 && f[0] == client {
			return f[1], f[2], nil
		}
	}
	return "", "", fmt.Errorf("no client %q", client)
}
