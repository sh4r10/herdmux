package popup

import (
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestClientResolution(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	other := s.Tmux("new-session", "-d", "-s", "other", "-P", "-F", "#{pane_id}")
	c := s.Attach("main")

	if got, err := Client(s.Client, env(nil), c.TTY); err != nil || got != c.TTY {
		t.Errorf("--client: %q %v", got, err)
	}
	if _, err := Client(s.Client, env(nil), "/dev/nope"); err == nil || !strings.Contains(err.Error(), "no client") {
		t.Errorf("unknown --client: %v", err)
	}
	if got, err := Client(s.Client, env(map[string]string{EnvClient: c.TTY}), ""); err != nil || got != c.TTY {
		t.Errorf("HERDMUX_CLIENT: %q %v", got, err)
	}
	if got, err := Client(s.Client, env(map[string]string{"TMUX_PANE": s.Pane}), ""); err != nil || got != c.TTY {
		t.Errorf("from the pane: %q %v", got, err)
	}
	// No client shows session "other": the pane's session decides, not any client.
	if _, err := Client(s.Client, env(map[string]string{"TMUX_PANE": other}), ""); err == nil {
		t.Error("a pane in a session with no client resolved a client")
	}
	if _, err := Client(s.Client, env(nil), ""); err == nil {
		t.Error("nothing to go on resolved a client")
	}
}

func TestInside(t *testing.T) {
	t.Parallel()
	if Inside(env(nil)) || !Inside(env(map[string]string{EnvPopup: "1"})) {
		t.Error("Inside")
	}
}

func TestShowErrorIsRed(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set-option", "-g", "@herdmux_c_attention_needs_input", "#d14d41")
	c := s.Attach("main")
	ShowError(s.Client, c.TTY, "herdmux launch: no agent #1")
	// tmux draws the message in the client's status line: red, with # intact.
	s.Eventually(3*time.Second, "the red message", func() bool {
		out := c.Output()
		// The client's TERM has no RGB, so tmux sends #d14d41 as colour 167.
		return strings.Contains(out, "\x1b[38;5;167m") && strings.Contains(out, "herdmux launch: no agent #1")
	})
}
