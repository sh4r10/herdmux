package main

import (
	"flag"

	"github.com/sh4r10/herdmux/internal/attention"
	"github.com/sh4r10/herdmux/internal/logfile"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/popup"
)

// next-attention and the cross-session bell.

// defineNextAttention jumps the client to the most urgent agent with
// Attention, across sessions (needs-input first, oldest first). With none
// it shows a brief message and the client stays put.
func defineNextAttention(fs *flag.FlagSet) func(*env, []string) error {
	clientName := clientFlag(fs)
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("next-attention takes no arguments")
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		client, err := popup.Client(c, e.getenv, *clientName)
		if err != nil {
			return err
		}
		return showOnClient(e, c, client, "next-attention", nextAttention(c, client))
	}
}

func nextAttention(c *tmux.Client, client string) error {
	a, ok, err := attention.Next(c)
	if err != nil {
		return err
	}
	if !ok {
		return attention.ShowNone(c, client)
	}
	if err := attention.Jump(c, client, a.Pane); err != nil {
		return err
	}
	// The pane-focus-in hook makes this a Visit too; clearing here as well
	// means the flag is gone when the verb returns.
	return state.Visit(c, a.Window)
}

// ring rings the bell for this run's Attention raises (one bell and one
// message per run). A failure is logged, never returned: the state report
// itself succeeded.
func ring(e *env, c *tmux.Client, outcomes ...state.Outcome) {
	var b attention.Bell
	for _, o := range outcomes {
		b.Add(o)
	}
	if err := b.Ring(c); err != nil {
		_ = logfile.Printf(e.getenv, "bell: %v", err)
	}
}
