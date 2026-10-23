package attention

import (
	"strconv"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// The elsewhere indicator: per-session options that the status bar's right
// side reads. For each session they describe the most urgent agent with
// Attention in the *other* sessions (the tabs show this session's own), in
// Order: needs-input before done, oldest first. All three are unset when no
// other session has Attention.
const (
	OptElsewhereKind    = "@herdmux_elsewhere_kind"    // needs-input | done
	OptElsewhereSession = "@herdmux_elsewhere_session" // that agent's session name
	OptElsewhereMore    = "@herdmux_elsewhere_more"    // how many more agents elsewhere have Attention; unset for none
)

var sessionFormat = tmux.Fields("#{session_id}", "#{session_name}",
	"#{"+OptElsewhereKind+"}", "#{"+OptElsewhereSession+"}", "#{"+OptElsewhereMore+"}")

// Sync recomputes the elsewhere options of every session from the agents'
// Attention, writing only what changed, in one tmux call. It does not
// reconcile; callers run Reconcile first. Run it after anything that can
// change Attention: a report, a Visit, a reconcile.
func Sync(c *tmux.Client) error {
	agents, err := state.List(c)
	if err != nil {
		return err
	}
	flagged := Order(agents)
	out, err := c.RunList("list-sessions", "-F", sessionFormat)
	if err != nil {
		return err
	}
	var cmds [][]string
	for _, line := range tmux.Lines(out) {
		f := tmux.Split(line)
		if len(f) != 5 {
			continue
		}
		id, name := f[0], f[1]
		var kind, session, more string
		n := 0
		for _, a := range flagged {
			if a.Session == name {
				continue
			}
			if n == 0 {
				kind, session = a.Attention, a.Session
			}
			n++
		}
		if n > 1 {
			more = strconv.Itoa(n - 1)
		}
		put := func(opt, was, now string) {
			switch {
			case was == now:
			case now == "":
				cmds = append(cmds, []string{"set-option", "-u", "-t", id, opt})
			default:
				cmds = append(cmds, []string{"set-option", "-t", id, opt, tmux.Literal(now)})
			}
		}
		put(OptElsewhereKind, f[2], kind)
		put(OptElsewhereSession, f[3], session)
		put(OptElsewhereMore, f[4], more)
	}
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
	_, err = c.Run(args...)
	return err
}
