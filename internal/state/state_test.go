package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// agentPane opens a background window whose pane runs a long sleep, standing
// in for an agent, and returns the pane id and the sleep's pid (the pane's
// foreground process).
func agentPane(t *testing.T, s *tmuxtest.Server) (string, int) {
	t.Helper()
	pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "sleep 100000")
	return pane, panePID(t, s, pane)
}

func panePID(t *testing.T, s *tmuxtest.Server, pane string) int {
	t.Helper()
	pid, err := strconv.Atoi(s.Display(pane, "#{pane_pid}"))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// opts returns the five options of a pane, in AllOpts order, joined by "|".
func opts(t *testing.T, s *tmuxtest.Server, pane string) string {
	t.Helper()
	var v []string
	for _, o := range AllOpts {
		v = append(v, s.PaneOption(pane, o))
	}
	return strings.Join(v, "|")
}

func apply(t *testing.T, s *tmuxtest.Server, r Report) Outcome {
	t.Helper()
	o, err := Apply(s.Client, r)
	if err != nil {
		t.Fatalf("Apply(%+v): %v", r, err)
	}
	return o
}

func reconcile(t *testing.T, s *tmuxtest.Server) {
	t.Helper()
	if err := Reconcile(s.Client); err != nil {
		t.Fatal(err)
	}
}

var t0 = time.Unix(1_800_000_000, 0)

func TestReportWritesAllFiveAndSinceOnlyOnChange(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := agentPane(t, s)
	p := strconv.Itoa(pid)

	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Working, PID: pid, Now: t0})
	if got, want := opts(t, s, pane), "claude|working||1800000000|"+p; got != want {
		t.Fatalf("after working: %q, want %q", got, want)
	}
	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Working, PID: pid, Now: t0.Add(10 * time.Second)})
	if got := s.PaneOption(pane, OptSince); got != "1800000000" {
		t.Fatalf("since changed on a repeat: %q", got)
	}
	o := apply(t, s, Report{Pane: pane, Agent: "claude", Event: Done, PID: pid, Now: t0.Add(20 * time.Second)})
	if got, want := opts(t, s, pane), "claude|done|done|1800000020|"+p; got != want {
		t.Fatalf("after done: %q, want %q", got, want)
	}
	if !o.Raised || o.Attention != Done || o.Session != tmuxtest.Session || !strings.HasPrefix(o.Window, "@") {
		t.Fatalf("outcome %+v", o)
	}

	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Exit, PID: pid})
	if got := opts(t, s, pane); got != "||||" {
		t.Fatalf("after exit: %q", got)
	}
}

func TestReportRejectsBadInput(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	for _, r := range []Report{
		{Pane: s.Pane, Agent: "", Event: Working},
		{Pane: s.Pane, Agent: "bot", Event: Working},
		{Pane: s.Pane, Agent: "pi", Event: Unknown},
		{Pane: "", Agent: "pi", Event: Working},
		{Pane: "%999", Agent: "pi", Event: Working},
	} {
		if _, err := Apply(s.Client, r); err == nil {
			t.Errorf("Apply(%+v) succeeded", r)
		}
	}
}

func TestAttention(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := agentPane(t, s) // a background window: unwatched
	step := func(ev, wantAtt string, wantRaised bool) {
		t.Helper()
		o := apply(t, s, Report{Pane: pane, Agent: "opencode", Event: ev, PID: pid})
		if got := s.PaneOption(pane, OptAttention); got != wantAtt || o.Raised != wantRaised {
			t.Fatalf("%s: attention=%q raised=%v, want %q %v", ev, got, o.Raised, wantAtt, wantRaised)
		}
	}
	step(Done, "", false)               // first registration never raises
	step(Working, "", false)            //
	step(NeedsInput, NeedsInput, true)  // working → needs-input raises
	step(Done, Done, true)              // kind follows; a change into done raises again
	step(NeedsInput, NeedsInput, true)  //
	step(NeedsInput, NeedsInput, false) // no change, no raise
	step(Working, NeedsInput, false)    // working keeps the flag and its kind
	step(Done, Done, true)              // working → done

	if err := Visit(s.Client, s.Display(pane, "#{window_id}")); err != nil {
		t.Fatal(err)
	}
	if got := s.PaneOption(pane, OptAttention); got != "" {
		t.Fatalf("after Visit: %q", got)
	}
	if s.PaneOption(pane, OptState) != Done {
		t.Fatal("Visit changed the state")
	}
}

func TestAttentionNotRaisedFromUnknown(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "exec bash -c 'exec -a pi sleep 100000'")
	s.Eventually(2*time.Second, "pi command", func() bool { return s.Display(pane, "#{pane_current_command}") == "pi" })
	reconcile(t, s)
	if got := s.PaneOption(pane, OptState); got != Unknown {
		t.Fatalf("state %q, want unknown", got)
	}
	o := apply(t, s, Report{Pane: pane, Agent: "pi", Event: Done, PID: panePID(t, s, pane)})
	if o.Raised || s.PaneOption(pane, OptAttention) != "" || s.PaneOption(pane, OptState) != Done {
		t.Fatalf("unknown → done: %+v %q", o, opts(t, s, pane))
	}
	// The report wins: the next reconcile keeps it reporting.
	reconcile(t, s)
	if got := s.PaneOption(pane, OptState); got != Done {
		t.Fatalf("after reconcile: %q", got)
	}
}

func TestAttentionNotRaisedWhileWatched(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane := s.Tmux("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", s.Pane, "sleep 100000")
	pid := panePID(t, s, pane)
	s.Attach(tmuxtest.Session) // shows the window holding both panes
	if s.Display(pane, "#{window_active_clients}") != "1" {
		t.Fatal("window not watched")
	}
	for _, ev := range []string{Working, NeedsInput, Working, Done} {
		if o := apply(t, s, Report{Pane: pane, Agent: "claude", Event: ev, PID: pid}); o.Raised {
			t.Fatalf("%s raised Attention while watched", ev)
		}
	}
	if got := s.PaneOption(pane, OptAttention); got != "" {
		t.Fatalf("attention %q while watched", got)
	}
}

func TestDeathDropsAgentSilently(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}") // an interactive /bin/sh
	s.Tmux("send-keys", "-t", pane, "sleep 100000", "Enter")
	s.Eventually(2*time.Second, "sleep in the foreground", func() bool {
		return s.Display(pane, "#{pane_current_command}") == "sleep"
	})
	fg, err := foregroundPgrp(panePID(t, s, pane))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Working, PID: fg})
	reconcile(t, s)
	if s.PaneOption(pane, OptState) != Working {
		t.Fatal("live foreground agent was dropped")
	}
	if err := syscall.Kill(fg, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	s.Eventually(2*time.Second, "the agent to die", func() bool { return !alive(fg) })
	reconcile(t, s)
	if got := opts(t, s, pane); got != "||||" {
		t.Fatalf("after death: %q", got)
	}
}

func TestReconcileDropsDeadAndBackgroundPids(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)

	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	bg := exec.Command("sleep", "100000") // alive, but not in any pane
	if err := bg.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bg.Process.Kill(); _ = bg.Wait() })

	p1, _ := agentPane(t, s)
	p2, _ := agentPane(t, s)
	p3, live := agentPane(t, s)
	apply(t, s, Report{Pane: p1, Agent: "claude", Event: NeedsInput, PID: dead.Process.Pid})
	apply(t, s, Report{Pane: p2, Agent: "pi", Event: Working, PID: bg.Process.Pid})
	apply(t, s, Report{Pane: p3, Agent: "opencode", Event: Working, PID: live})
	reconcile(t, s)
	if got := opts(t, s, p1); got != "||||" {
		t.Errorf("dead pid kept: %q", got)
	}
	if got := opts(t, s, p2); got != "||||" {
		t.Errorf("background pid kept: %q", got)
	}
	if s.PaneOption(p3, OptState) != Working {
		t.Errorf("live agent dropped: %q", opts(t, s, p3))
	}
}

func TestReconcileUnknownByCommand(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	for _, tc := range []struct{ name, agent string }{
		{"claude", "claude"}, {"codex", "codex"}, {"opencode", "opencode"}, {"pi", "pi"}, {"2.1.300", "claude"}, {"node", ""},
	} {
		pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}") // an interactive /bin/sh
		s.Tmux("send-keys", "-t", pane, "bash -c 'exec -a "+tc.name+" sleep 100000'", "Enter")
		s.Eventually(2*time.Second, tc.name+" in the foreground", func() bool {
			return s.Display(pane, "#{pane_current_command}") == tc.name
		})
		reconcile(t, s)
		fg, err := foregroundPgrp(panePID(t, s, pane))
		if err != nil {
			t.Fatal(err)
		}
		want := "||||"
		if tc.agent != "" {
			want = tc.agent + "|unknown||" + s.PaneOption(pane, OptSince) + "|" + strconv.Itoa(fg)
			if s.PaneOption(pane, OptSince) == "" {
				t.Errorf("%s: no since", tc.name)
			}
		}
		if got := opts(t, s, pane); got != want {
			t.Errorf("%s: %q, want %q", tc.name, got, want)
		}

		// Killing the process drops the shell back into the foreground.
		if err := syscall.Kill(fg, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		s.Eventually(2*time.Second, "the shell back in the foreground", func() bool {
			return s.Display(pane, "#{pane_current_command}") == "sh"
		})
		reconcile(t, s)
		if got := opts(t, s, pane); got != "||||" {
			t.Errorf("%s after kill: %q", tc.name, got)
		}
	}
}

// An npm-installed codex shows as node (its wrapper) in pane_current_command;
// the codex child makes the pane an unknown codex.
func TestReconcileNodeWrapsCodex(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	codex := filepath.Join(t.TempDir(), "codex") // a sleep whose comm and argv[0] are codex
	if err := os.Symlink("/bin/sleep", codex); err != nil {
		t.Fatal(err)
	}
	pane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}")
	s.Tmux("send-keys", "-t", pane, `bash -c 'exec -a node bash -c "`+codex+` 100000; :"'`, "Enter")
	s.Eventually(2*time.Second, "node in the foreground", func() bool {
		return s.Display(pane, "#{pane_current_command}") == "node"
	})
	s.Eventually(2*time.Second, "codex under node", func() bool {
		reconcile(t, s)
		return s.PaneOption(pane, OptAgent) == "codex"
	})
	if got := s.PaneOption(pane, OptState); got != Unknown {
		t.Errorf("state %q, want unknown", got)
	}
}

func TestMatchCommand(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]string{
		"claude": "claude", "2.1.300": "claude", "10.0.1": "claude", "codex": "codex", "opencode": "opencode", "pi": "pi",
		"node": "", "bash": "", "2.1": "", "v2.1.300": "", "pi2": "", "codex.js": "", "": "",
	} {
		if got := MatchCommand(cmd); got != want {
			t.Errorf("MatchCommand(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestOptionsSurviveMoves(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := agentPane(t, s)
	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Working, PID: pid, Now: t0})
	apply(t, s, Report{Pane: pane, Agent: "claude", Event: Done, PID: pid, Now: t0})
	want := opts(t, s, pane)
	check := func(what string) {
		t.Helper()
		if got := opts(t, s, pane); got != want {
			t.Fatalf("after %s: %q, want %q", what, got, want)
		}
	}
	s.Tmux("join-pane", "-d", "-s", pane, "-t", s.Pane)
	check("join-pane")
	s.Tmux("break-pane", "-d", "-s", pane)
	check("break-pane")
	s.Tmux("new-session", "-d", "-s", "other")
	s.Tmux("move-window", "-d", "-s", s.Display(pane, "#{window_id}"), "-t", "other:")
	check("move-window")
	reconcile(t, s)
	check("reconcile")
}

func TestVisitClearsOnlyThatWindow(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	a, apid := agentPane(t, s)
	b := s.Tmux("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", a, "sleep 100000")
	c, cpid := agentPane(t, s)
	for _, r := range []Report{
		{Pane: a, Agent: "claude", Event: Working, PID: apid},
		{Pane: a, Agent: "claude", Event: NeedsInput, PID: apid},
		{Pane: b, Agent: "pi", Event: Working, PID: panePID(t, s, b)},
		{Pane: b, Agent: "pi", Event: Done, PID: panePID(t, s, b)},
		{Pane: c, Agent: "pi", Event: Working, PID: cpid},
		{Pane: c, Agent: "pi", Event: Done, PID: cpid},
	} {
		apply(t, s, r)
	}
	if err := Visit(s.Client, s.Display(a, "#{window_id}")); err != nil {
		t.Fatal(err)
	}
	if s.PaneOption(a, OptAttention) != "" || s.PaneOption(b, OptAttention) != "" {
		t.Fatal("Visit left Attention in its window")
	}
	if s.PaneOption(c, OptAttention) != Done {
		t.Fatal("Visit cleared another window")
	}
	if err := Visit(s.Client, "%1"); err == nil {
		t.Fatal("Visit accepted a pane id")
	}
}

func TestListAndInit(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := agentPane(t, s)
	apply(t, s, Report{Pane: pane, Agent: "pi", Event: Working, PID: pid, Now: t0})
	apply(t, s, Report{Pane: pane, Agent: "pi", Event: Done, PID: pid, Now: t0})
	got, err := List(s.Client)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("List: %+v", got)
	}
	want := Agent{Session: "main", WindowIndex: 1, WindowName: s.Display(pane, "#{window_name}"), Window: s.Display(pane, "#{window_id}"),
		Pane: pane, Agent: "pi", State: Done, Attention: Done, Since: t0.Unix(), PID: pid}
	if got[0] != want {
		t.Fatalf("List: %+v, want %+v", got[0], want)
	}

	for range 2 {
		if err := Init(s.Client); err != nil {
			t.Fatal(err)
		}
	}
	if v := s.Tmux("show-options", "-gv", "focus-events"); v != "on" {
		t.Errorf("focus-events %q", v)
	}
	if v := s.Tmux("show-environment", "-g", "HERDMUX"); v != "HERDMUX=1" {
		t.Errorf("HERDMUX: %q", v)
	}
	hooks := s.Tmux("show-hooks", "-g", "pane-focus-in")
	if want := "pane-focus-in[77] run-shell -b \"herdmux tmux-hook pane-focus-in #{window_id}\""; hooks != want {
		t.Errorf("hooks %q, want %q", hooks, want)
	}
}

// A shell reporting for itself (`herdmux report` typed at a prompt) stays
// while it runs a foreground child; a suspended agent is dropped.
func TestForegroundOwnership(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	shellPane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}") // an interactive /bin/sh
	agentPane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}")
	for _, p := range []string{shellPane, agentPane} {
		s.Tmux("send-keys", "-t", p, "sleep 100000", "Enter")
		s.Eventually(2*time.Second, "sleep in the foreground", func() bool {
			return s.Display(p, "#{pane_current_command}") == "sleep"
		})
	}
	apply(t, s, Report{Pane: shellPane, Agent: "claude", Event: Working, PID: panePID(t, s, shellPane)})
	sleep, err := foregroundPgrp(panePID(t, s, agentPane))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, Report{Pane: agentPane, Agent: "claude", Event: Working, PID: sleep})
	reconcile(t, s)
	if s.PaneOption(shellPane, OptState) != Working || s.PaneOption(agentPane, OptState) != Working {
		t.Fatalf("dropped: shell %q, agent %q", opts(t, s, shellPane), opts(t, s, agentPane))
	}

	if err := syscall.Kill(sleep, syscall.SIGTSTP); err != nil { // Ctrl-Z: the shell takes the foreground
		t.Fatal(err)
	}
	s.Eventually(2*time.Second, "the shell back in the foreground", func() bool {
		return s.Display(agentPane, "#{pane_current_command}") == "sh"
	})
	reconcile(t, s)
	if got := opts(t, s, agentPane); got != "||||" {
		t.Fatalf("suspended agent kept: %q", got)
	}
	_ = syscall.Kill(sleep, syscall.SIGKILL)
}
