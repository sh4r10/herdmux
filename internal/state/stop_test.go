package state

import (
	"errors"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

func TestStop(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	// A shell with a typed agent: the agent's group is stopped, the shell stays.
	pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}")
	s.Tmux("send-keys", "-t", pane, "-l", "bash -c 'exec -a claude sleep 100000'")
	s.Tmux("send-keys", "-t", pane, "Enter")
	s.Eventually(3*time.Second, "the fake claude", func() bool { return s.Display(pane, "#{pane_current_command}") == "claude" })
	reconcile(t, s)
	if s.PaneOption(pane, OptAgent) != "claude" {
		t.Fatal("not an agent")
	}
	shell := panePID(t, s, pane)
	if err := Stop(s.Client, pane); err != nil {
		t.Fatal(err)
	}
	if got := opts(t, s, pane); got != "||||" {
		t.Errorf("after stop: %q", got)
	}
	if panePID(t, s, pane) != shell || s.Display(pane, "#{pane_dead} #{pane_current_command}") != "0 sh" {
		t.Errorf("pane after stop: %s", s.Display(pane, "#{pane_dead} #{pane_current_command}"))
	}

	// An agent that is the pane's own process (no shell under it), or an idle
	// shell: refused, nothing is signalled.
	own, pid := agentPane(t, s)
	if err := Stop(s.Client, own); !errors.Is(err, ErrPaneProcess) {
		t.Errorf("pane's own process: %v", err)
	}
	if !alive(pid) {
		t.Error("the pane's own process was signalled")
	}
	if err := Stop(s.Client, pane); !errors.Is(err, ErrPaneProcess) {
		t.Errorf("idle shell: %v", err)
	}
	if err := Stop(s.Client, "%999"); err == nil {
		t.Error("missing pane: no error")
	}
}
