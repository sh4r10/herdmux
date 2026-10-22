package state

import (
	"strings"

	"herdmux/internal/tmux"
)

// Agent is one agent pane as List returns it. The JSON form is what
// `herdmux list --json` prints and what MCP list_agents mirrors.
type Agent struct {
	Session     string `json:"session"`      // #{session_name}
	WindowIndex int    `json:"window_index"` // #{window_index}
	WindowName  string `json:"window_name"`  // #{window_name}
	Window      string `json:"window"`       // #{window_id}, @N
	Pane        string `json:"pane"`         // #{pane_id}, %N
	Agent       string `json:"agent"`        // claude | opencode | pi
	State       string `json:"state"`        // working | needs-input | done | unknown
	Attention   string `json:"attention"`    // "" | needs-input | done
	Since       int64  `json:"since"`        // epoch seconds of the last state change
	PID         int    `json:"pid"`          // 0 if unknown
}

// window_name goes last, so a tab in a window name can't shift the fields.
var listFormat = tmux.Fields("#{session_name}", "#{window_index}", "#{window_id}", "#{pane_id}",
	"#{"+OptAgent+"}", "#{"+OptState+"}", "#{"+OptAttention+"}", "#{"+OptSince+"}", "#{"+OptPID+"}", "#{window_name}")

// List returns every agent on the server in tmux's order (sessions by name,
// windows by index, panes by index). It does not reconcile; callers run
// Reconcile first.
func List(c *tmux.Client) ([]Agent, error) {
	out, err := c.RunList("list-panes", "-a", "-F", listFormat)
	if err != nil {
		return nil, err
	}
	agents := []Agent{}
	for _, line := range tmux.Lines(out) {
		f := strings.SplitN(line, tmux.FieldSep, 10)
		if len(f) != 10 || f[4] == "" {
			continue
		}
		agents = append(agents, Agent{
			Session: f[0], WindowIndex: atoi(f[1]), Window: f[2], Pane: f[3], Agent: f[4],
			State: f[5], Attention: f[6], Since: int64(atoi(f[7])), PID: atoi(f[8]), WindowName: f[9],
		})
	}
	return agents, nil
}
