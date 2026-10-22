// Package attention acts on Attention across sessions: `herdmux
// next-attention` (Order, Next, Jump) and the cross-session bell and message
// (Bell).
//
// The bell: a pane's own BEL never leaves an unattached session, so herdmux
// picks one client itself and writes \a straight to its #{client_tty}, then
// shows that client a transient display-message: the state icon in its
// Attention colour and the session name. tmux's bell-action, visual-bell and
// monitor-bell are not involved.
package attention

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"herdmux/internal/colorscheme"
	"herdmux/internal/state"
	"herdmux/internal/tmux"
	"herdmux/internal/ui/style"
)

// Order returns the agents with Attention, most urgent first: needs-input
// before done, then oldest @herdmux_since first. Equal ones keep their
// (tmux) order. No separate raise time is stored, so "oldest" is the last
// state change.
func Order(agents []state.Agent) []state.Agent {
	var out []state.Agent
	for _, a := range agents {
		if a.Attention != "" {
			out = append(out, a)
		}
	}
	rank := func(a state.Agent) int {
		if a.Attention == state.NeedsInput {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(a, b state.Agent) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.Since, b.Since))
	})
	return out
}

// Next is the most urgent agent with Attention on the server; ok is false
// when there is none. It does not reconcile; callers run Reconcile first.
func Next(c *tmux.Client) (a state.Agent, ok bool, err error) {
	agents, err := state.List(c)
	if err != nil {
		return state.Agent{}, false, err
	}
	if o := Order(agents); len(o) > 0 {
		return o[0], true, nil
	}
	return state.Agent{}, false, nil
}

// Jump switches client to pane (%N), across sessions, and selects its window
// and the pane. The pane-focus-in hook then makes it a Visit.
func Jump(c *tmux.Client, client, pane string) error {
	_, err := c.Run("switch-client", "-c", client, "-t", pane, ";",
		"select-window", "-t", pane, ";", "select-pane", "-t", pane)
	return err
}

// NoneMessage is what next-attention shows when no agent has Attention.
const NoneMessage = "herdmux: no agent needs attention"

// ShowNone shows NoneMessage on client.
func ShowNone(c *tmux.Client, client string) error {
	_, err := c.Run("display-message", "-c", client, NoneMessage)
	return err
}

// Bell collects the Attention raises of one herdmux run, so that several
// raises give one bell and one message. The zero value is ready to use.
type Bell struct {
	raises []raise
}

type raise struct{ session, kind string }

// Add records a state.Apply outcome; outcomes that raised nothing are
// ignored. A repeat raise on an already-flagged agent counts.
func (b *Bell) Add(o state.Outcome) {
	if o.Raised && o.Session != "" {
		b.raises = append(b.raises, raise{o.Session, o.Attention})
	}
}

// Client is one attached client as the bell sees it.
type Client struct {
	Name     string // #{client_name}
	TTY      string // #{client_tty}
	Session  string // #{session_name}
	Focused  bool   // client_flags contains "focused"
	Activity int64  // #{client_activity}, epoch seconds
}

var clientFormat = tmux.Fields("#{client_name}", "#{client_tty}", "#{client_flags}", "#{client_activity}", "#{session_name}")

// Clients lists the attached clients.
func Clients(c *tmux.Client) ([]Client, error) {
	out, err := c.RunList("list-clients", "-F", clientFormat)
	if err != nil {
		return nil, err
	}
	var cs []Client
	for _, line := range tmux.Lines(out) {
		f := strings.SplitN(line, tmux.FieldSep, 5)
		if len(f) != 5 {
			continue
		}
		act, _ := strconv.ParseInt(f[3], 10, 64)
		focused := slices.Contains(strings.Split(f[2], ","), "focused")
		cs = append(cs, Client{Name: f[0], TTY: f[1], Focused: focused, Activity: act, Session: f[4]})
	}
	return cs, nil
}

// Pick chooses the one client that rings: the focused client or, with none
// focused, the most recently active one. lookedAt is the focused client's
// session ("" with none focused: then no session counts as looked at).
// Several focused clients (tmux marks a client focused on attach, before
// any focus report) are told apart by activity too. ok is false with no
// clients.
func Pick(cs []Client) (pick Client, lookedAt string, ok bool) {
	best := -1
	for i, cl := range cs {
		if best < 0 || better(cl, cs[best]) {
			best = i
		}
	}
	if best < 0 {
		return Client{}, "", false
	}
	pick = cs[best]
	if pick.Focused {
		lookedAt = pick.Session
	}
	return pick, lookedAt, true
}

func better(a, b Client) bool {
	if a.Focused != b.Focused {
		return a.Focused
	}
	return a.Activity > b.Activity
}

// Ring rings for the collected raises outside the looked-at session: one
// BEL on the picked client's tty and one message on that client listing
// each such session once (with the latest raise's icon). It does nothing
// when every raise is in the looked-at session (the tab colour shows it)
// or no client is attached.
func (b *Bell) Ring(c *tmux.Client) error {
	if len(b.raises) == 0 {
		return nil
	}
	cs, err := Clients(c)
	if err != nil {
		return err
	}
	pick, lookedAt, ok := Pick(cs)
	if !ok {
		return nil
	}
	var sessions []string
	kinds := map[string]string{}
	for _, r := range b.raises {
		if r.session == lookedAt {
			continue
		}
		if _, seen := kinds[r.session]; !seen {
			sessions = append(sessions, r.session)
		}
		kinds[r.session] = r.kind
	}
	if len(sessions) == 0 {
		return nil
	}
	errBell := writeBEL(pick.TTY)
	_, errMsg := c.Run("display-message", "-c", pick.Name, tmux.Literal(Message(sessions, kinds)))
	return errors.Join(errBell, errMsg)
}

// Message is the bell's display-message format: per session, the state icon
// in its Attention colour (attention_needs_input / attention_done) and the
// session name in the message style, e.g. "<icon> api  <icon> web".
func Message(sessions []string, kinds map[string]string) string {
	parts := make([]string, len(sessions))
	for i, s := range sessions {
		slot := "attention_done"
		if kinds[s] == state.NeedsInput {
			slot = "attention_needs_input"
		}
		ref := colorscheme.Ref(slot)
		parts[i] = "#[fg=#{?" + ref + "," + ref + ",default}]" + style.StateIcon(kinds[s]) +
			"#[default] " + strings.ReplaceAll(s, "#", "##")
	}
	return strings.Join(parts, "  ")
}

// writeBEL writes one \a to a client's terminal. Only ttys tmux listed for
// a client get here (and in tests the tmux wrapper reaches only
// herdmux-test-* servers, whose clients are test ptys).
func writeBEL(tty string) error {
	if !strings.HasPrefix(tty, "/dev/") {
		return fmt.Errorf("bell: not a tty %q", tty)
	}
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("bell: %w", err)
	}
	defer f.Close()
	_ = f.SetWriteDeadline(time.Now().Add(time.Second)) // never hang on a stuck terminal
	if _, err := f.Write([]byte{'\a'}); err != nil {
		return fmt.Errorf("bell: %w", err)
	}
	return nil
}
