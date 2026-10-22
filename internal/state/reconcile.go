package state

import (
	"regexp"
	"strconv"
	"time"

	"herdmux/internal/tmux"
)

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// MatchCommand maps a #{pane_current_command} to an agent name, or "" for
// none. A semver name is claude started by its versioned path.
// `node` (pi's first ~1 s) matches nothing, so it is picked up on a later run.
func MatchCommand(cmd string) string {
	switch {
	case cmd == "claude", semver.MatchString(cmd):
		return "claude"
	case cmd == "opencode", cmd == "pi":
		return cmd
	}
	return ""
}

var reconcileFormat = tmux.Fields("#{pane_id}", "#{pane_pid}", "#{pane_dead}", "#{pane_current_command}",
	"#{"+OptAgent+"}", "#{"+OptState+"}", "#{"+OptPID+"}")

// Reconcile brings every pane on the server in line with its processes, with
// one list-panes -a plus /proc reads and at most one write call:
//
//   - A reporting agent (state other than unknown) whose pid is dead, or not
//     in the pane's foreground process group, is dropped: all five options
//     are unset. No Attention is raised for a death.
//   - A pane with no live reporting agent whose foreground command matches an
//     agent (MatchCommand) becomes that agent in state unknown; when the
//     command stops matching, it is unset again.
//
// Every herdmux invocation calls it first.
func Reconcile(c *tmux.Client) error {
	out, err := c.RunList("list-panes", "-a", "-F", reconcileFormat)
	if err != nil {
		return err
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	var w writer
	for _, line := range tmux.Lines(out) {
		f := tmux.Split(line)
		if len(f) != 7 {
			continue
		}
		pane, panePID, dead, cmd, agent, st, pid := f[0], atoi(f[1]), f[2] == "1", f[3], f[4], f[5], atoi(f[6])
		if dead {
			if agent != "" || st != "" || pid != 0 {
				w.unsetAll(pane)
			}
			continue
		}
		if st != "" && st != Unknown && agent != "" {
			if inForeground(pid, panePID) {
				continue // a live reporting agent: reports own it
			}
			// Dropped; fall through so a matching command can become unknown.
			agent, st = "", ""
			w.unsetAll(pane)
		}
		want := MatchCommand(cmd)
		switch {
		case want == "" && (agent != "" || st != ""):
			w.unsetAll(pane)
		case want != "" && (agent != want || st != Unknown):
			w.unsetAll(pane)
			w.set(pane, OptAgent, want)
			w.set(pane, OptState, Unknown)
			w.set(pane, OptSince, now)
			if fg, err := foregroundPgrp(panePID); err == nil {
				w.set(pane, OptPID, strconv.Itoa(fg))
			}
		}
	}
	return w.flush(c)
}
