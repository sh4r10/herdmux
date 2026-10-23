package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// End-to-end tests of the state verbs: the built binary against a tmuxtest
// server, as tmux hooks and agent integrations run it.

// sleeper opens a background window running a long sleep (a stand-in agent)
// and returns its pane id and pane pid (the pane's foreground process).
func sleeper(t *testing.T, s *tmuxtest.Server) (pane, pid string) {
	t.Helper()
	pane = s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "sleep 100000")
	return pane, s.Display(pane, "#{pane_pid}")
}

func herdmuxOK(t *testing.T, s *tmuxtest.Server, args ...string) tmuxtest.Result {
	t.Helper()
	r := s.Herdmux(args...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("herdmux %v: code=%d stderr=%q", args, r.Code, r.Stderr)
	}
	return r
}

func TestReportCLI(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := sleeper(t, s)

	// --pane overrides $TMUX_PANE (s.Pane); flags may follow the state.
	herdmuxOK(t, s, "report", "working", "--agent", "claude", "--pid", pid, "--pane", pane)
	if got := s.PaneOption(pane, state.OptState) + "|" + s.PaneOption(pane, state.OptPID); got != "working|"+pid {
		t.Fatalf("report: %q", got)
	}
	if s.PaneOption(s.Pane, state.OptAgent) != "" {
		t.Fatal("report wrote $TMUX_PANE despite --pane")
	}

	// $TMUX_PANE is the default pane; the default pid is report's parent.
	r := s.Run(tmuxtest.Cmd{Args: []string{"report", "done", "--agent", "pi"}, Pane: pane})
	if r.Code != 0 || s.PaneOption(pane, state.OptState) != "done" || s.PaneOption(pane, state.OptPID) != itoa(os.Getpid()) {
		t.Fatalf("default pane/pid: code=%d %q %q", r.Code, s.PaneOption(pane, state.OptState), s.PaneOption(pane, state.OptPID))
	}

	herdmuxOK(t, s, "report", "exit", "--agent", "pi", "--pane", pane)
	if s.PaneOption(pane, state.OptAgent) != "" {
		t.Fatal("exit left the agent")
	}
}

func TestReportCLIErrorsExit0AndLog(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	for _, args := range [][]string{
		{"report", "working"},                   // missing --agent
		{"report", "busy", "--agent", "claude"}, // bad state
		{"report", "done", "--agent", "bot"},    // bad agent
		{"report", "done", "--agent", "claude", "--pane", "%999"},
		{"tmux-hook", "no-such-event"},
	} {
		r := s.Herdmux(args...)
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, r.Code, r.Stdout, r.Stderr)
		}
	}
	log, _ := os.ReadFile(s.LogPath())
	for _, want := range []string{"--agent is required", `unknown state "busy"`, `unknown agent "bot"`, "%999", `unknown event "no-such-event"`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if s.PaneOption(s.Pane, state.OptAgent) != "" {
		t.Error("a failed report wrote state")
	}
}

func TestInitCLI(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set-hook", "-g", "pane-focus-in", "display-message mine") // the user's own hook
	for range 2 {
		herdmuxOK(t, s, "init")
	}
	if v := s.Tmux("show-options", "-gv", "focus-events"); v != "on" {
		t.Errorf("focus-events %q", v)
	}
	if v := s.Tmux("show-environment", "-g", "HERDMUX"); v != "HERDMUX=1" {
		t.Errorf("HERDMUX: %q", v)
	}
	hooks := s.Tmux("show-hooks", "-g", "pane-focus-in")
	if strings.Count(hooks, "herdmux tmux-hook pane-focus-in") != 1 || !strings.Contains(hooks, "display-message mine") {
		t.Errorf("hooks after two inits:\n%s", hooks)
	}
}

// TestVisitThroughHook drives the real path: init installs the hook, an
// attached client selects a window, tmux fires pane-focus-in, and the hook's
// `herdmux tmux-hook` clears Attention in that window only.
func TestVisitThroughHook(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "init")
	a, apid := sleeper(t, s)
	b := s.Tmux("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", a, "sleep 100000")
	bpid := s.Display(b, "#{pane_pid}")
	c, cpid := sleeper(t, s)
	for _, p := range [][2]string{{a, apid}, {b, bpid}, {c, cpid}} {
		herdmuxOK(t, s, "report", "working", "--agent", "claude", "--pane", p[0], "--pid", p[1])
	}
	herdmuxOK(t, s, "report", "needs-input", "--agent", "claude", "--pane", a, "--pid", apid)
	herdmuxOK(t, s, "report", "done", "--agent", "claude", "--pane", b, "--pid", bpid)
	herdmuxOK(t, s, "report", "done", "--agent", "claude", "--pane", c, "--pid", cpid)
	for p, want := range map[string]string{a: "needs-input", b: "done", c: "done"} {
		if got := s.PaneOption(p, state.OptAttention); got != want {
			t.Fatalf("%s attention %q, want %q", p, got, want)
		}
	}

	s.Attach(tmuxtest.Session) // shows window 0; fires pane-focus-in there
	s.Tmux("select-window", "-t", s.Display(a, "#{window_id}"))
	s.Eventually(3*time.Second, "the Visit to clear window a", func() bool {
		return s.PaneOption(a, state.OptAttention) == "" && s.PaneOption(b, state.OptAttention) == ""
	})
	time.Sleep(200 * time.Millisecond) // give a wrong clear the chance to happen
	if got := s.PaneOption(c, state.OptAttention); got != "done" {
		t.Fatalf("other window's attention %q", got)
	}
	if got := s.PaneOption(b, state.OptState); got != "done" {
		t.Fatalf("Visit changed state: %q", got)
	}
}

func TestListCLI(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if r := herdmuxOK(t, s, "list"); r.Stdout != "no agents\n" {
		t.Fatalf("empty list: %q", r.Stdout)
	}
	if r := herdmuxOK(t, s, "list", "--json"); r.Stdout != "[]\n" {
		t.Fatalf("empty list --json: %q", r.Stdout)
	}
	pane, pid := sleeper(t, s)
	s.Tmux("rename-window", "-t", pane, "agent")
	herdmuxOK(t, s, "report", "working", "--agent", "opencode", "--pane", pane, "--pid", pid)
	herdmuxOK(t, s, "report", "needs-input", "--agent", "opencode", "--pane", pane, "--pid", pid)

	out := herdmuxOK(t, s, "list").Stdout
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || strings.Join(strings.Fields(lines[0]), " ") != "SESSION WINDOW AGENT STATE ATTENTION PANE" ||
		strings.Join(strings.Fields(lines[1]), " ") != "main 1:agent opencode needs-input needs-input "+pane {
		t.Fatalf("list:\n%s", out)
	}

	var agents []state.Agent
	if err := json.Unmarshal([]byte(herdmuxOK(t, s, "list", "--json").Stdout), &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Pane != pane || agents[0].Agent != "opencode" || agents[0].State != "needs-input" ||
		agents[0].Attention != "needs-input" || agents[0].Session != "main" || agents[0].WindowName != "agent" ||
		itoa(agents[0].PID) != pid || agents[0].Since == 0 {
		t.Fatalf("list --json: %+v", agents)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
