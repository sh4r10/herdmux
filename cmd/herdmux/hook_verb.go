package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/sh4r10/herdmux/internal/integrations/claude"
)

// maxHookInput caps the hook JSON read from stdin. Events carry tool inputs
// and the last assistant message, so allow plenty.
const maxHookInput = 16 << 20

// defineHook is `herdmux hook claude|codex`: one hook event on stdin becomes
// a state report for $TMUX_PANE, in-process. codex's hooks speak claude's
// protocol, so both go through internal/integrations/claude with the agent
// name. It is hidden and neverFail: it prints nothing, always exits 0 and
// logs errors.
//
// The gate: without HERDMUX=1 and TMUX_PANE it does nothing at all (no tmux
// call, no log line), so the globally installed hooks stay silent in agent
// sessions outside the layer.
func defineHook(fs *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) != 1 || (args[0] != "claude" && args[0] != "codex") {
			return usagef("want: hook claude|codex")
		}
		agent := args[0]
		// Drain stdin even when gated off, so the agent never writes into a closed pipe.
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
		pid := claude.Find(e.ppid, agent)
		if pid == 0 {
			return fmt.Errorf("%s: no %s process above pid %d", ev.Name, agent, e.ppid)
		}
		o, err := claude.Handle(c, agent, e.getenv("TMUX_PANE"), pid, ev)
		if err == nil {
			ring(e, c, o)
		}
		return err
	}
}
