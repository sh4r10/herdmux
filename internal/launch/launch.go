// Package launch starts an agent from a config.toml [[agent]] entry: a new
// window named after the entry, in the target session and directory, with the
// command typed into its interactive shell, so the shell stays open after the
// agent exits. The client then jumps straight to it.
//
// Launch adds nothing to the environment: the dev flags come from the
// herdmux-dev shims and HERDMUX=1 from `herdmux init`. It is shared by
// `herdmux launch`, its prompt popup and the picker's `n`.
package launch

import (
	"errors"
	"fmt"
	"strings"

	"herdmux/internal/config"
	"herdmux/internal/tmux"
)

// Target is where a launch goes.
type Target struct {
	Client  string // the client to switch to the new window ("": none)
	Session string // session id ("$1")
	Dir     string // the new window's directory
}

// ClientTarget is client's current session and its active pane's directory.
// It reads list-clients: `display -c` would expand in the caller's pane
// ($TMUX_PANE), not the client's.
func ClientTarget(c *tmux.Client, client string) (Target, error) {
	out, err := c.Run("list-clients", "-F", tmux.Fields("#{client_name}", "#{session_id}", "#{pane_current_path}"))
	if err != nil {
		return Target{}, err
	}
	for _, line := range tmux.Lines(out) {
		if name, rest, _ := strings.Cut(line, tmux.FieldSep); name == client {
			return parseTarget(client, rest)
		}
	}
	return Target{}, fmt.Errorf("no client %q", client)
}

// PaneTarget is pane's session and directory, switching client (may be "").
func PaneTarget(c *tmux.Client, pane, client string) (Target, error) {
	out, err := c.Display(pane, tmux.Fields("#{pane_id}", "#{session_id}", "#{pane_current_path}"))
	if err != nil {
		return Target{}, err
	}
	id, rest, _ := strings.Cut(out, tmux.FieldSep)
	if id != pane { // display -t on a missing pane succeeds with an empty context
		return Target{}, fmt.Errorf("no pane %q", pane)
	}
	return parseTarget(client, rest)
}

func parseTarget(client, out string) (Target, error) {
	f := tmux.Split(out)
	if len(f) != 2 || f[0] == "" {
		return Target{}, errors.New("no session to launch into")
	}
	return Target{Client: client, Session: f[0], Dir: f[1]}, nil
}

// Launch opens a window named a.Name in t.Session at t.Dir, types a.Command
// into its shell, and switches t.Client to it. It returns the new pane id.
// new-window expands formats in -n and -c, so both are escaped.
func Launch(c *tmux.Client, a config.Agent, t Target) (string, error) {
	args := []string{"new-window", "-t", t.Session + ":", "-n", tmux.FormatLiteral(a.Name), "-P", "-F", "#{pane_id}"}
	if t.Dir != "" {
		args = append(args, "-c", tmux.FormatLiteral(t.Dir))
	}
	pane, err := c.Run(args...)
	if err != nil {
		return "", err
	}
	if _, err := c.Run("send-keys", "-t", pane, "-l", tmux.Literal(a.Command), ";", "send-keys", "-t", pane, "Enter"); err != nil {
		return pane, err
	}
	if t.Client != "" {
		if err := Switch(c, t.Client, pane); err != nil {
			return pane, err
		}
	}
	return pane, nil
}

// Switch shows pane on client: its session, window and pane, in one tmux call
// (also the picker's jump). Showing the window is a Visit, so the
// pane-focus-in hook clears its Attention.
func Switch(c *tmux.Client, client, pane string) error {
	_, err := c.Run("switch-client", "-c", client, "-t", pane, ";",
		"select-window", "-t", pane, ";", "select-pane", "-t", pane)
	return err
}
