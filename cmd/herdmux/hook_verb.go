package main

import (
	"flag"
	"fmt"
	"io"

	"herdmux/internal/integrations/claude"
)

// maxHookInput caps the hook JSON read from stdin. Events carry tool inputs
// and the last assistant message, so allow plenty.
const maxHookInput = 16 << 20

// defineHook is `herdmux hook claude`: one claude hook event
// on stdin becomes a state report for $TMUX_PANE, in-process. It is hidden and
// neverFail: it prints nothing, always exits 0 and logs errors.
//
// The gate: without HERDMUX=1 and TMUX_PANE it does nothing at all (no tmux
// call, no log line), so the globally installed hooks stay silent in claude
// sessions outside the layer.
func defineHook(fs *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) != 1 || args[0] != "claude" {
			return usagef("want: hook claude")
		}
		// Drain stdin even when gated off, so claude never writes into a closed pipe.
		in, err := io.ReadAll(io.LimitReader(e.stdin, maxHookInput))
		if e.getenv("HERDMUX") != "1" || e.getenv("TMUX_PANE") == "" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		ev, err := claude.Parse(in)
		if err != nil {
			return fmt.Errorf("bad hook JSON: %w", err)
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		if claude.Decide(ev) == claude.Ignore {
			return nil
		}
		pid := claude.FindClaude(e.ppid)
		if pid == 0 {
			return fmt.Errorf("%s: no claude process above pid %d", ev.Name, e.ppid)
		}
		o, err := claude.Handle(c, e.getenv("TMUX_PANE"), pid, ev)
		if err == nil {
			ring(e, c, o)
		}
		return err
	}
}
