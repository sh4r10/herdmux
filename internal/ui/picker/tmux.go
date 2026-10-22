package picker

import (
	"fmt"
	"strings"

	"herdmux/internal/state"
	"herdmux/internal/tmux"
	"herdmux/internal/ui/split"
)

// The picker's remembered toggles: server-global runtime options (lost with
// the server, never written into tmux.conf). Unset is the default.
const (
	OptScope = "@herdmux_picker_scope" // "session" after Tab; unset: all sessions
	OptSort  = "@herdmux_picker_sort"  // "attention" after s; unset: by place
)

// TmuxBackend is the Backend on a real server.
type TmuxBackend struct{ C *tmux.Client }

func (b TmuxBackend) Agents() ([]state.Agent, error) { return state.List(b.C) }

func (b TmuxBackend) Capture(pane string, n int) []string {
	lines, _ := split.Capture(b.C, pane, n) // a pane that just went away shows nothing
	return lines
}

func (b TmuxBackend) Stop(a state.Agent) error { return state.Stop(b.C, a.Pane) }

func (b TmuxBackend) Save(s Settings) { _ = SaveSettings(b.C, s) }

// LoadSettings reads the remembered toggles.
func LoadSettings(c *tmux.Client) (Settings, error) {
	out, err := c.Display("", tmux.Fields("#{"+OptScope+"}", "#{"+OptSort+"}"))
	if err != nil {
		return Settings{}, err
	}
	f := tmux.Split(out)
	for len(f) < 2 {
		f = append(f, "")
	}
	return Settings{ThisSession: f[0] == "session", AttentionFirst: f[1] == "attention"}, nil
}

// SaveSettings remembers the toggles, in one tmux call.
func SaveSettings(c *tmux.Client, s Settings) error {
	opt := func(name string, on bool, val string) []string {
		if on {
			return []string{"set-option", "-g", name, val}
		}
		return []string{"set-option", "-gu", name}
	}
	args := append(opt(OptScope, s.ThisSession, "session"), ";")
	_, err := c.Run(append(args, opt(OptSort, s.AttentionFirst, "attention")...)...)
	return err
}

// ClientSession is the name of the session client shows. It reads
// list-clients: `display -c` would expand in the caller's pane.
func ClientSession(c *tmux.Client, client string) (string, error) {
	out, err := c.Run("list-clients", "-F", tmux.Fields("#{client_name}", "#{session_name}"))
	if err != nil {
		return "", err
	}
	for _, line := range tmux.Lines(out) {
		if name, sess, _ := strings.Cut(line, tmux.FieldSep); name == client {
			return sess, nil
		}
	}
	return "", fmt.Errorf("no client %q", client)
}
