package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// The state verbs: report, list, init and tmux-hook. Each
// one reconciles first, as every herdmux invocation does.

// reconciled returns a client for $TMUX after reconciling every pane.
func reconciled(e *env) (*tmux.Client, error) {
	c, err := tmux.FromEnv(e.getenv)
	if err != nil {
		return nil, err
	}
	return c, state.Reconcile(c)
}

func defineReport(fs *flag.FlagSet) func(*env, []string) error {
	agent := fs.String("agent", "", "the agent's name: claude, opencode or pi (required)")
	pid := fs.Int("pid", 0, "the agent's pid (default: report's parent pid)")
	pane := fs.String("pane", "", "the pane id (default: $TMUX_PANE)")
	return func(e *env, args []string) error {
		if len(args) != 1 {
			return usagef("want exactly one state: working, needs-input, done or exit")
		}
		if *agent == "" {
			return usagef("--agent is required")
		}
		r := state.Report{Agent: *agent, Event: args[0], Pane: *pane, PID: *pid}
		if r.Pane == "" {
			r.Pane = e.getenv("TMUX_PANE")
		}
		if r.PID == 0 {
			r.PID = e.ppid
		}
		if !state.IsEvent(r.Event) {
			return usagef("unknown state %q (want working, needs-input, done or exit)", r.Event)
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		o, err := state.Apply(c, r)
		if err == nil {
			ring(e, c, o)
		}
		return err
	}
}

func defineList(fs *flag.FlagSet) func(*env, []string) error {
	asJSON := fs.Bool("json", false, "print JSON for scripts")
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("list takes no arguments")
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		agents, err := state.List(c)
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(e.stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(agents)
		}
		if len(agents) == 0 {
			fmt.Fprintln(e.stdout, "no agents")
			return nil
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SESSION\tWINDOW\tAGENT\tSTATE\tATTENTION\tPANE")
		for _, a := range agents {
			att := a.Attention
			if att == "" {
				att = "-"
			}
			fmt.Fprintf(tw, "%s\t%d:%s\t%s\t%s\t%s\t%s\n", a.Session, a.WindowIndex, a.WindowName, a.Agent, a.State, att, a.Pane)
		}
		return tw.Flush()
	}
}

func defineInit(fs *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("init takes no arguments")
		}
		c, err := tmux.FromEnv(e.getenv)
		if err != nil {
			return err
		}
		// initStatusbar applies the colorscheme (05) first, so the keys' red
		// config error is drawn in the scheme's colour.
		if err := initStatusbar(e, c); err != nil {
			return err
		}
		initKeys(e, c)
		if err := state.Init(c); err != nil {
			return err
		}
		return state.Reconcile(c)
	}
}

func defineTmuxHook(fs *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) == 0 {
			return usagef("missing event")
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		switch ev := args[0]; ev {
		case "pane-focus-in": // <window_id>: a Visit
			if len(args) != 2 {
				return usagef("pane-focus-in wants the window id")
			}
			return state.Visit(c, args[1])
		default:
			if ok, err := statusbarHook(c, ev, args[1:]); ok {
				return err
			}
			return fmt.Errorf("unknown event %q", ev)
		}
	}
}
