package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/attention"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// End-to-end tests of next-attention and the bell that report and hook claude
// ring.

// sleeperIn is sleeper in another session.
func sleeperIn(t *testing.T, s *tmuxtest.Server, session string) (pane, pid string) {
	t.Helper()
	pane = s.Tmux("new-window", "-d", "-t", session+":", "-P", "-F", "#{pane_id}", "sleep 100000")
	return pane, s.Display(pane, "#{pane_pid}")
}

func raise(t *testing.T, s *tmuxtest.Server, pane, pid, kind string) {
	t.Helper()
	herdmuxOK(t, s, "report", "working", "--agent", "claude", "--pane", pane, "--pid", pid)
	herdmuxOK(t, s, "report", kind, "--agent", "claude", "--pane", pane, "--pid", pid)
	if got := s.PaneOption(pane, state.OptAttention); got != kind {
		t.Fatalf("%s attention %q, want %q", pane, got, kind)
	}
}

func TestNextAttentionCrossesSessions(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "init")
	s.Tmux("new-session", "-d", "-s", "other")
	done, donePID := sleeperIn(t, s, "main") // a background window
	ask, askPID := sleeperIn(t, s, "other")
	raise(t, s, done, donePID, state.Done)
	raise(t, s, ask, askPID, state.NeedsInput)
	c := s.Attach("main")

	where := func() string { return c.Format("#{session_name} #{pane_id}") }
	next := func() {
		t.Helper()
		if r := s.Herdmux("next-attention", "--client", c.TTY); r.Code != 0 || r.Stderr != "" {
			t.Fatalf("next-attention: code=%d stderr=%q", r.Code, r.Stderr)
		}
	}

	next() // needs-input first, though it's newer and in another session
	if got := where(); got != "other "+ask {
		t.Fatalf("after first jump: %q", got)
	}
	s.Eventually(2*time.Second, "the Visit", func() bool { return s.PaneOption(ask, state.OptAttention) == "" })

	next()
	if got := where(); got != "main "+done {
		t.Fatalf("after second jump: %q", got)
	}
	s.Eventually(2*time.Second, "the Visit", func() bool { return s.PaneOption(done, state.OptAttention) == "" })

	next() // none left: a message, and the client stays put
	s.Eventually(2*time.Second, "the none message", func() bool { return strings.Contains(c.Output(), attention.NoneMessage) })
	if got := where(); got != "main "+done {
		t.Fatalf("with none, the client moved: %q", got)
	}
}

func TestNextAttentionNeedsTmux(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if r := s.Run(tmuxtest.Cmd{Args: []string{"next-attention"}, Outside: true}); r.Code != 2 {
		t.Fatalf("outside tmux: code=%d stderr=%q", r.Code, r.Stderr)
	}
}

// TestReportAndHookRing: a raise in another session rings the attached
// client once per raise (repeats too) and shows "<icon> <session>"; a raise
// in another window of the client's own session rings nothing.
func TestReportAndHookRing(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "focus-events", "on")
	s.Tmux("new-session", "-d", "-s", "api")
	c := s.Attach("main")
	bels := func() int { return strings.Count(c.Output(), "\a") }

	pane, pid := sleeperIn(t, s, "api")
	raise(t, s, pane, pid, state.Done)
	s.Eventually(2*time.Second, "the bell", func() bool { return bels() == 1 })
	s.Eventually(2*time.Second, "the message", func() bool { return strings.Contains(c.Output(), style.IconDone+" api") })

	raise(t, s, pane, pid, state.Done) // a repeat raise on a flagged agent
	s.Eventually(2*time.Second, "the second bell", func() bool { return bels() == 2 })

	local, lpid := sleeperIn(t, s, "main") // a background window
	raise(t, s, local, lpid, state.NeedsInput)

	// hook claude rings too.
	cl, clpid := fakeClaude(t, s)
	s.Tmux("move-window", "-s", cl, "-t", "api:")
	hookInProcess(t, s, cl, clpid, `{"hook_event_name":"SessionStart","source":"startup"}`)
	hookInProcess(t, s, cl, clpid, `{"hook_event_name":"UserPromptSubmit","prompt":"x"}`)
	hookInProcess(t, s, cl, clpid, `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	s.Eventually(2*time.Second, "the hook's bell", func() bool { return bels() == 3 })
	s.Eventually(2*time.Second, "the hook's message", func() bool {
		return strings.Contains(c.Output(), style.IconNeedsInput+" api")
	})
	time.Sleep(200 * time.Millisecond)
	if n := bels(); n != 3 {
		t.Fatalf("%d BELs, want 3 (the main-session raise must not ring)", n)
	}
}

// TestInitBeforeAnySessionBindsKeys: tmux.conf's run-shell 'herdmux init'
// runs at server start, before the first session exists, when a bare
// `display -p` fails with "no current target". prefix u must still be bound.
func TestInitBeforeAnySessionBindsKeys(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "exit-empty", "off")
	s.Tmux("kill-session", "-t", tmuxtest.Session)
	r := s.Run(tmuxtest.Cmd{Args: []string{"init"}, Env: []string{"TMUX_PANE="}})
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("init: code=%d stderr=%q", r.Code, r.Stderr)
	}
	if got := s.Tmux("list-keys", "-T", "prefix", "u"); !strings.Contains(got, "herdmux next-attention") {
		t.Fatalf("prefix u: %q (log %q)", got, readFile(t, s.LogPath()))
	}
}
