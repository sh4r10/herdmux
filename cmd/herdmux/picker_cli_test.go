package main

import (
	"strings"
	"testing"
	"time"

	"herdmux/internal/state"
	"herdmux/internal/tmuxtest"
)

// End-to-end tests of the agent picker popup: the real
// popup on an attached client, one key per send-keys.

// typedAgent opens a background window in session whose interactive shell
// runs a process named claude in the foreground (as a launch types the
// command into the shell), and waits until reconcile has it as an agent.
func typedAgent(t *testing.T, s *tmuxtest.Server, session string) string {
	t.Helper()
	pane := s.Tmux("new-window", "-d", "-t", session+":", "-P", "-F", "#{pane_id}")
	s.Tmux("send-keys", "-t", pane, "-l", "bash -c 'exec -a claude sleep 100000'")
	s.Tmux("send-keys", "-t", pane, "Enter")
	s.Eventually(3*time.Second, "the fake claude", func() bool {
		return s.Display(pane, "#{pane_current_command}") == "claude"
	})
	herdmuxOK(t, s, "list") // reconciles
	if got := s.PaneOption(pane, state.OptAgent); got != "claude" {
		t.Fatalf("agent %q", got)
	}
	return pane
}

func key(s *tmuxtest.Server, c *tmuxtest.Client, k string) {
	s.Tmux("send-keys", "-K", "-c", c.TTY, k)
}

func openPicker(t *testing.T, s *tmuxtest.Server, c *tmuxtest.Client) {
	t.Helper()
	start := time.Now()
	herdmuxOK(t, s, "picker", "--client", c.TTY)
	s.Eventually(5*time.Second, "the picker", func() bool {
		return strings.Contains(c.Output(), "by place")
	})
	t.Logf("picker drawn %v after the verb started", time.Since(start).Round(time.Millisecond))
	time.Sleep(150 * time.Millisecond) // bubbletea's first frame is up; keys now reach it
}

// The smoke run: two sessions of agents, the client on main. By place, main's
// agent is first, so j selects the other session's agent, which has
// Attention; ⏎ switches the client there and the Visit clears it.
func TestPickerJumpSmoke(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("new-session", "-d", "-s", "other")
	c := s.Attach("main")
	herdmuxOK(t, s, "init")
	typedAgent(t, s, "main")
	target := typedAgent(t, s, "other")
	pid := s.Display(target, "#{pane_pid}")
	for _, st := range []string{"working", "needs-input"} { // registration raises nothing; the change does
		if r := s.Run(tmuxtest.Cmd{Args: []string{"report", st, "--agent", "claude", "--pid", pid}, Pane: target}); r.Code != 0 {
			t.Fatalf("report: %+v", r)
		}
	}
	// The pane's shell owns the foreground (it is the sleep's ancestor), so the report sticks.
	if got := s.PaneOption(target, state.OptAttention); got != state.NeedsInput {
		t.Fatalf("attention %q", got)
	}

	openPicker(t, s, c)
	key(s, c, "j")
	time.Sleep(100 * time.Millisecond)
	key(s, c, "Enter")
	s.Eventually(5*time.Second, "the client on the other session's agent", func() bool {
		return c.Format("#{pane_id}") == target
	})
	if got := c.Format("#{session_name}"); got != "other" {
		t.Errorf("session %q", got)
	}
	s.Eventually(3*time.Second, "the Visit to clear Attention", func() bool {
		return s.PaneOption(target, state.OptAttention) == ""
	})
}

// x then y stops the agent: the pane stays with its shell, and the agent is
// unset by the reconcile that follows.
func TestPickerStop(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach("main")
	pane := typedAgent(t, s, "main")
	panePID := s.Display(pane, "#{pane_pid}")

	openPicker(t, s, c)
	key(s, c, "x")
	s.Eventually(3*time.Second, "the stop confirm", func() bool {
		return strings.Contains(c.Output(), "(pane stays)")
	})
	key(s, c, "y")
	s.Eventually(5*time.Second, "the agent to be stopped", func() bool {
		return s.PaneOption(pane, state.OptAgent) == ""
	})
	if got := s.Display(pane, "#{pane_id} #{pane_dead} #{pane_pid} #{pane_current_command}"); got != pane+" 0 "+panePID+" sh" {
		t.Errorf("after the stop: %q", got)
	}
	s.Eventually(3*time.Second, "the stopped message", func() bool {
		return strings.Contains(c.Output(), "stopped claude in main:")
	})
	key(s, c, "q")
}
