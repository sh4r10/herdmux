// Package state holds agent state: the
// five @herdmux_* pane options, report (the one writer), reconcile, Attention
// and Visit.
//
// Every herdmux invocation that touches tmux calls Reconcile first. Agent
// state then changes only through:
//
//   - Report: an integration reports a state (`herdmux report`, and in-process
//     from `hook claude`). It alone decides whether Attention is raised.
//   - Visit: a window was shown in an attached client (`tmux-hook
//     pane-focus-in`). It clears Attention on every pane of that window.
//   - Reconcile: drops dead or backgrounded reporting agents, and marks or
//     unmarks `unknown` agents by their foreground process name.
//
// No other package may write the five options. Readers (tabs, picker, MCP)
// read them with List or with tmux formats over the Opt* names.
package state

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"herdmux/internal/tmux"
)

// The five pane options, all written by this package only.
const (
	OptAgent     = "@herdmux_agent"     // claude | opencode | pi
	OptState     = "@herdmux_state"     // working | needs-input | done | unknown
	OptAttention = "@herdmux_attention" // "" | needs-input | done
	OptSince     = "@herdmux_since"     // epoch seconds of the last state change
	OptPID       = "@herdmux_pid"       // the agent process pid
)

// AllOpts lists the five options in a fixed order.
var AllOpts = []string{OptAgent, OptState, OptAttention, OptSince, OptPID}

// Agent states, as stored in OptState.
const (
	Working    = "working"
	NeedsInput = "needs-input"
	Done       = "done"
	Unknown    = "unknown"
)

// Exit is the report event that removes the agent from its pane.
const Exit = "exit"

// Agent names, as stored in OptAgent.
var Agents = []string{"claude", "opencode", "pi"}

// IsAgent reports whether name is a known agent name.
func IsAgent(name string) bool {
	for _, a := range Agents {
		if a == name {
			return true
		}
	}
	return false
}

// IsEvent reports whether ev is a valid report event: a reportable state or
// Exit. (Unknown is not reportable; only Reconcile sets it.)
func IsEvent(ev string) bool {
	switch ev {
	case Working, NeedsInput, Done, Exit:
		return true
	}
	return false
}

// writer batches set/unset commands into one tmux call (commands joined
// with a ";" argument). If the batch fails (e.g. a pane vanished meanwhile,
// which aborts the rest of a tmux command sequence), each command is retried
// on its own so the other panes still get written.
type writer struct{ cmds [][]string }

func (w *writer) add(cmd ...string) { w.cmds = append(w.cmds, cmd) }

func (w *writer) set(pane, opt, val string) { w.add("set-option", "-p", "-t", pane, opt, val) }
func (w *writer) unset(pane, opt string)    { w.add("set-option", "-p", "-u", "-t", pane, opt) }

func (w *writer) unsetAll(pane string) {
	for _, o := range AllOpts {
		w.unset(pane, o)
	}
}

func (w *writer) flush(c *tmux.Client) error {
	cmds := w.cmds
	w.cmds = nil
	if len(cmds) == 0 {
		return nil
	}
	var args []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, cmd...)
	}
	if _, err := c.Run(args...); err == nil || len(cmds) == 1 {
		return err
	}
	var errs []error
	for _, cmd := range cmds {
		if _, err := c.Run(cmd...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// checkPane rejects anything that is not a pane id (%N).
func checkPane(p string) error {
	if len(p) < 2 || p[0] != '%' {
		return fmt.Errorf("invalid pane %q (want %%N)", p)
	}
	if _, err := strconv.Atoi(p[1:]); err != nil {
		return fmt.Errorf("invalid pane %q (want %%N)", p)
	}
	return nil
}
