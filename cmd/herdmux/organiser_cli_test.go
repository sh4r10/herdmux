package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// End-to-end test of the window organiser popup: the real
// popup on an attached client, one key per send-keys.

func windowOrder(s *tmuxtest.Server) string {
	return s.Tmux("list-windows", "-t", "main", "-F", "#{window_index}:#{window_id}")
}

// The smoke run: with base-index 1 and renumber-windows on, open the
// organiser on window 2 (its cursor starts on the current window), then
// space, j, space carries it to 3. The tabs moved, indexes are gapless and
// the client is still on the same window.
func TestOrganiserReorderSmoke(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "base-index", "1")
	s.Tmux("set", "-g", "renumber-windows", "on")
	s.Tmux("move-window", "-r", "-t", "main")
	for _, n := range []string{"b", "c"} {
		s.Tmux("new-window", "-d", "-t", "main:", "-n", n)
	}
	c := s.Attach("main")
	herdmuxOK(t, s, "init")
	s.Tmux("select-window", "-t", "main:2")
	w1, w2, w3 := s.Display("main:1", "#{window_id}"), s.Display("main:2", "#{window_id}"), s.Display("main:3", "#{window_id}")

	herdmuxOK(t, s, "organiser", "--client", c.TTY)
	s.Eventually(5*time.Second, "the organiser", func() bool {
		return strings.Contains(c.Output(), "space grab")
	})
	time.Sleep(150 * time.Millisecond) // bubbletea's first frame is up; keys now reach it
	for _, k := range []string{"Space", "j", "Space"} {
		key(s, c, k)
		time.Sleep(100 * time.Millisecond)
	}
	want := "1:" + w1 + "\n2:" + w3 + "\n3:" + w2
	s.Eventually(5*time.Second, "the reorder", func() bool { return windowOrder(s) == want })
	if got := c.Format("#{window_id}"); got != w2 {
		t.Errorf("the client is on %s, want %s (unchanged)", got, w2)
	}
	if got := c.Format("#{window_index}"); got != "3" {
		t.Errorf("the current window is at %s, want 3", got)
	}
	key(s, c, "q")
	// prefix l still goes to the window the client was on before (1).
	s.Tmux("last-window", "-t", "main")
	if got := c.Format("#{window_id}"); got != w1 {
		t.Errorf("last-window went to %s, want %s", got, w1)
	}
}
