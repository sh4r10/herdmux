package colorscheme

import (
	"github.com/sh4r10/herdmux/internal/tmux"
)

// OptActive (global, runtime-only) names the scheme the server shows now.
const OptActive = "@herdmux_colorscheme"

// StyleOptions are the tmux style options a scheme sets, in order, as
// {option, value}. Everything tmux draws is covered; the
// herdmux formats read the @herdmux_c_<slot> options instead. Inactive panes
// aren't dimmed: window-style and window-active-style are the same.
func StyleOptions(s Scheme) [][2]string {
	c := s.Colors
	pane := "bg=" + c["pane_bg"] + ",fg=" + c["fg"]
	return [][2]string{
		{"status-style", "bg=" + c["bg"] + ",fg=" + c["fg"]},
		{"window-status-style", "bg=" + c["bg"] + ",fg=" + c["fg_muted"]},
		{"window-status-current-style", "bg=" + c["bg_raised"] + ",fg=" + c["fg"] + ",bold"},
		{"window-status-activity-style", "fg=" + c["accent"]},
		{"window-status-bell-style", "fg=" + c["attention_needs_input"]},
		{"pane-border-style", "fg=" + c["border"]},
		{"pane-active-border-style", "fg=" + c["border_active"]},
		{"popup-style", "bg=" + c["bg"] + ",fg=" + c["fg"]},
		{"popup-border-style", "fg=" + c["border"]},
		{"menu-style", "bg=" + c["bg_overlay"] + ",fg=" + c["fg"]},
		{"menu-selected-style", "bg=" + c["bg_raised"] + ",fg=" + c["fg"] + ",bold"},
		{"menu-border-style", "bg=" + c["bg_overlay"] + ",fg=" + c["border"]},
		{"message-style", "bg=" + c["bg_overlay"] + ",fg=" + c["fg"]},
		{"message-command-style", "bg=" + c["bg_overlay"] + ",fg=" + c["accent"]},
		{"mode-style", "bg=" + c["accent"] + ",fg=" + c["fg_on_attention"]},
		{"copy-mode-match-style", "bg=" + c["bg_raised"] + ",fg=" + c["fg"]},
		{"copy-mode-current-match-style", "bg=" + c["accent"] + ",fg=" + c["fg_on_attention"]},
		{"clock-mode-colour", c["accent"]},
		{"display-panes-colour", c["fg_muted"]},
		{"display-panes-active-colour", c["accent"]},
		{"window-style", pane},
		{"window-active-style", pane},
	}
}

// Apply restyles the whole server in one tmux call: the 16 @herdmux_c_<slot>
// options, @herdmux_colorscheme and every StyleOptions option, all global. The
// status formats read the slot options, so nothing is re-rendered.
func Apply(c *tmux.Client, s Scheme) error {
	if !s.Valid() {
		return s.Err()
	}
	var args []string
	set := func(opt, val string) {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "set-option", "-g", opt, val)
	}
	for _, slot := range Slots {
		set(Option(slot), s.Colors[slot])
	}
	set(OptActive, s.Name)
	for _, kv := range StyleOptions(s) {
		set(kv[0], kv[1])
	}
	_, err := c.Run(args...)
	return err
}

// Active is the scheme the server shows now ("" before any apply).
func Active(c *tmux.Client) (string, error) {
	return c.Run("show-options", "-gqv", OptActive)
}

// ApplySaved is init's step: resolve the saved choice and apply it. When it
// falls back, it shows Warning as a tmux message (and returns it in r for the
// log); the saved file is untouched.
func ApplySaved(c *tmux.Client, st *Store) (Resolved, error) {
	r, err := st.Resolve()
	if err != nil {
		return r, err
	}
	if err := Apply(c, r.Scheme); err != nil {
		return r, err
	}
	if r.Warning != "" {
		Message(c, "herdmux: "+r.Warning)
	}
	return r, nil
}

// Set is "choose this scheme" (`herdmux colorscheme <name>`, the picker's
// Enter, MCP set_colorscheme): it refuses an unknown or invalid scheme, then
// applies it live and saves the name. Nothing is saved when it fails.
func Set(c *tmux.Client, st *Store, name string) (Scheme, error) {
	s, err := st.Get(name)
	if err != nil {
		return s, err
	}
	if !s.Valid() {
		return s, s.Err()
	}
	if err := Apply(c, s); err != nil {
		return s, err
	}
	return s, st.Save(name)
}

// Message shows msg on the server's clients for a few seconds (tmux picks
// the client, as for display-message from a hook). Errors (no client yet,
// e.g. during the first init) are ignored.
func Message(c *tmux.Client, msg string) {
	_, _ = c.Run("display-message", "-d", "8000", tmux.FormatLiteral(msg))
}
