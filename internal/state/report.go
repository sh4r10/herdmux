package state

import (
	"fmt"
	"strconv"
	"time"

	"github.com/sh4r10/herdmux/internal/tmux"
)

// Report is one state report for a pane: what `herdmux report` parses from
// its arguments, and what `hook claude` builds from a hook event.
type Report struct {
	Pane  string    // pane id, %N (the CLI defaults it to $TMUX_PANE)
	Agent string    // claude | opencode | pi; required, never inferred
	Event string    // working | needs-input | done | exit
	PID   int       // the agent's pid (the CLI defaults it to its parent pid)
	Now   time.Time // the report's time; zero means time.Now()
}

// Outcome says what a Report did, for callers that react to it (the
// cross-session bell).
type Outcome struct {
	Raised    bool   // Attention was raised by this report
	Attention string // the pane's Attention after the report ("" if none)
	Session   string // #{session_name} of the pane
	Window    string // #{window_id} of the pane
}

var reportFormat = tmux.Fields("#{pane_id}", "#{"+OptAgent+"}", "#{"+OptState+"}", "#{"+OptAttention+"}",
	"#{window_active_clients}", "#{session_name}", "#{window_id}")

// Apply writes one report into the pane's options. It is the only code that
// decides Attention. Callers run Reconcile first (as every herdmux
// invocation does); Apply itself reads only the one pane.
//
// Rules:
//   - exit unsets all five options; no Attention.
//   - A registration (the pane had no agent, a different agent, or an
//     unknown one) never raises Attention and starts with none.
//   - Otherwise Attention is raised on a change into needs-input or done
//     while the window is unwatched (window_active_clients == 0).
//   - While raised, its kind follows the latest needs-input/done state;
//     working keeps the flag and its kind. Only Visit clears it.
//   - @herdmux_since changes only when the state (or agent) changes.
func Apply(c *tmux.Client, r Report) (Outcome, error) {
	if err := checkPane(r.Pane); err != nil {
		return Outcome{}, err
	}
	if !IsAgent(r.Agent) {
		return Outcome{}, fmt.Errorf("unknown agent %q (want claude, opencode or pi)", r.Agent)
	}
	if !IsEvent(r.Event) {
		return Outcome{}, fmt.Errorf("unknown state %q (want working, needs-input, done or exit)", r.Event)
	}
	out, err := c.Display(r.Pane, reportFormat)
	if err != nil {
		return Outcome{}, err
	}
	f := tmux.Split(out)
	if len(f) != 7 || f[0] != r.Pane {
		return Outcome{}, fmt.Errorf("pane %s not found", r.Pane)
	}
	prevAgent, prevState, prevAtt, watched := f[1], f[2], f[3], atoi(f[4]) > 0
	o := Outcome{Session: f[5], Window: f[6]}

	var w writer
	if r.Event == Exit {
		w.unsetAll(r.Pane)
		return o, w.flush(c)
	}
	now := r.Now
	if now.IsZero() {
		now = time.Now()
	}
	registration := prevAgent != r.Agent || prevState == "" || prevState == Unknown
	changed := registration || prevState != r.Event
	alertable := r.Event == NeedsInput || r.Event == Done

	att := prevAtt
	switch {
	case registration:
		att = ""
	case changed && alertable && !watched:
		att, o.Raised = r.Event, true
	case att != "" && alertable:
		att = r.Event // the kind follows the latest needs-input/done
	}
	o.Attention = att

	w.set(r.Pane, OptAgent, r.Agent)
	w.set(r.Pane, OptState, r.Event)
	if att == "" {
		w.unset(r.Pane, OptAttention)
	} else {
		w.set(r.Pane, OptAttention, att)
	}
	if changed {
		w.set(r.Pane, OptSince, strconv.FormatInt(now.Unix(), 10))
	}
	if r.PID > 0 {
		w.set(r.Pane, OptPID, strconv.Itoa(r.PID))
	} else {
		w.unset(r.Pane, OptPID)
	}
	return o, w.flush(c)
}
