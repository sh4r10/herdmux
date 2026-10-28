package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// Tests of `herdmux hook claude` and `herdmux hook codex`. The replay tests
// feed the recorded fixtures (internal/integrations/claude/testdata) one
// event at a time into the CLI, in-process, with the hook's parent pid set
// to a fake claude process in the pane, and check the pane's state after
// each step.

const fixtures = "../../internal/integrations/claude/testdata"

// fakeClaude opens a background window whose foreground process is named
// claude (argv[0], as tmux and the pid walk see it), and returns the pane id
// and that process's pid.
func fakeClaude(t *testing.T, s *tmuxtest.Server) (pane string, pid int) {
	t.Helper()
	return fakeAgent(t, s, "claude")
}

func fakeAgent(t *testing.T, s *tmuxtest.Server, name string) (pane string, pid int) {
	t.Helper()
	pane = s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "exec bash -c 'exec -a "+name+" sleep 100000'")
	s.Eventually(2*time.Second, "fake "+name, func() bool { return s.Display(pane, "#{pane_current_command}") == name })
	pid, err := strconv.Atoi(s.Display(pane, "#{pane_pid}"))
	if err != nil {
		t.Fatal(err)
	}
	return pane, pid
}

// hookInProcess runs `herdmux hook claude` in-process as claude (ppid) would
// from pane, with HERDMUX=1 and the event on stdin.
func hookInProcess(t *testing.T, s *tmuxtest.Server, pane string, ppid int, event string) {
	t.Helper()
	hookAgentInProcess(t, s, "claude", pane, ppid, event)
}

func hookAgentInProcess(t *testing.T, s *tmuxtest.Server, agent, pane string, ppid int, event string) {
	t.Helper()
	vars := map[string]string{"HERDMUX": "1"}
	for _, kv := range s.Env(pane) {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	var out, errb bytes.Buffer
	code := run([]string{"hook", agent}, &env{
		stdin:  strings.NewReader(event),
		stdout: &out,
		stderr: &errb,
		getenv: func(k string) string { return vars[k] },
		ppid:   ppid,
	})
	if code != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("hook %s: code=%d stdout=%q stderr=%q", agent, code, out.String(), errb.String())
	}
}

// TestHookCodex: codex events (hand-written from the codex hooks reference:
// the same protocol as claude's, with Interrupt and no Notification or
// background_tasks) drive a codex-named process through a turn, a
// permission prompt, a question (request_user_input), an interrupt and
// exit. A claude process in the pane is not codex: the hook logs and
// writes nothing.
func TestHookCodex(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := fakeAgent(t, s, "codex")
	ev := func(js string) { t.Helper(); hookAgentInProcess(t, s, "codex", pane, pid, js) }
	want := func(st string) {
		t.Helper()
		if got := paneState(s, pane); got != st {
			t.Fatalf("%s, want %s", got, st)
		}
	}
	common := `"session_id":"s1","transcript_path":null,"cwd":"/tmp","model":"gpt-5","permission_mode":"default",`
	ev(`{` + common + `"hook_event_name":"SessionStart","source":"startup"}`)
	want("done/")
	if got := s.PaneOption(pane, state.OptAgent) + " " + s.PaneOption(pane, state.OptPID); got != "codex "+strconv.Itoa(pid) {
		t.Fatalf("agent/pid = %q", got)
	}
	ev(`{` + common + `"hook_event_name":"UserPromptSubmit","turn_id":"t1","prompt":"x"}`)
	want("working/")
	ev(`{` + common + `"hook_event_name":"PreToolUse","turn_id":"t1","tool_name":"Bash","tool_use_id":"c1","tool_input":{"command":"ls"}}`)
	ev(`{` + common + `"hook_event_name":"PermissionRequest","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	want("needs-input/needs-input")
	ev(`{` + common + `"hook_event_name":"PostToolUse","turn_id":"t1","tool_name":"Bash","tool_use_id":"c1","tool_input":{},"tool_response":{}}`)
	want("working/needs-input")
	ev(`{` + common + `"hook_event_name":"PreToolUse","turn_id":"t1","tool_name":"request_user_input","tool_use_id":"c2","tool_input":{}}`)
	want("needs-input/needs-input")
	ev(`{` + common + `"hook_event_name":"PostToolUse","turn_id":"t1","tool_name":"request_user_input","tool_use_id":"c2","tool_input":{},"tool_response":{}}`)
	want("working/needs-input")
	ev(`{` + common + `"hook_event_name":"Stop","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"OK"}`)
	want("done/done")
	ev(`{` + common + `"hook_event_name":"UserPromptSubmit","turn_id":"t2","prompt":"y"}`)
	want("working/done")
	ev(`{` + common + `"hook_event_name":"Interrupt","turn_id":"t2"}`)
	want("done/done")
	ev(`{` + common + `"hook_event_name":"SessionEnd","reason":"other"}`)
	want("-")
	if log, err := os.ReadFile(s.LogPath()); err == nil {
		t.Errorf("hook logged:\n%s", log)
	}

	// A claude in the pane is no codex: the hook writes nothing (reconcile
	// alone marks the claude-named process unknown) and logs.
	cpane, cpid := fakeClaude(t, s)
	hookAgentInProcess(t, s, "codex", cpane, cpid, `{"hook_event_name":"SessionStart","source":"startup"}`)
	if got := s.PaneOption(cpane, state.OptAgent) + " " + paneState(s, cpane); got != "claude unknown/" {
		t.Errorf("codex hook under claude: %s, want claude unknown/", got)
	}
	if log, _ := os.ReadFile(s.LogPath()); !strings.Contains(string(log), "no codex process above pid") {
		t.Errorf("log lacks the missing-codex line:\n%s", log)
	}

	// A hook run by codex's background server (argv has --managed-daemon)
	// reports nothing and logs what to do.
	dpane := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", `exec bash -c 'exec -a codex bash -c "sleep 100000; :" app-server --managed-daemon'`)
	s.Eventually(2*time.Second, "fake daemon", func() bool { return s.Display(dpane, "#{pane_current_command}") == "codex" })
	dpid, _ := strconv.Atoi(s.Display(dpane, "#{pane_pid}"))
	hookAgentInProcess(t, s, "codex", dpane, dpid, `{"hook_event_name":"UserPromptSubmit","prompt":"x"}`)
	if got := paneState(s, dpane); got != "unknown/" {
		t.Errorf("daemon hook: state %s, want unknown/", got)
	}
	if log, _ := os.ReadFile(s.LogPath()); !strings.Contains(string(log), "codex features disable daemon_auto_start") {
		t.Errorf("log lacks the background-server line:\n%s", log)
	}
}

func readFixture(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtures, name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// eventName pulls hook_event_name out of a fixture line, for messages.
func eventName(line string) string {
	_, rest, _ := strings.Cut(line, `"hook_event_name":"`)
	name, _, _ := strings.Cut(rest, `"`)
	return name
}

// paneState is "<state>/<attention>", or "-" when the pane has no agent.
func paneState(s *tmuxtest.Server, pane string) string {
	if s.PaneOption(pane, state.OptAgent) == "" {
		return "-"
	}
	return s.PaneOption(pane, state.OptState) + "/" + s.PaneOption(pane, state.OptAttention)
}

func TestHookClaudeReplay(t *testing.T) {
	t.Parallel()
	const (
		reg  = "done/"    // registered: done, no Attention
		w    = "working/" // working, no Attention
		ni   = "needs-input/needs-input"
		wNI  = "working/needs-input" // back to working; the flag stays
		dn   = "done/done"           // done, Attention raised
		wDn  = "working/done"
		gone = "-"
	)
	// The windows have no attached client, so every change into needs-input
	// or done raises Attention.
	scenarios := map[string][]string{
		// SessionStart, UserPromptSubmit, Stop, SessionEnd clear (kept),
		// SessionStart clear (stays done, keeps Attention), SessionEnd other.
		"prompt": {reg, w, dn, dn, dn, gone},
		// SessionStart, UserPromptSubmit, PreToolUse, PostToolUse,
		// PostToolBatch, Stop, internal SubagentStop with agent_id.
		"tool": {reg, w, w, w, w, dn, dn},
		// ... PreToolUse, PermissionRequest, Notification permission_prompt,
		// PostToolUse (approved), PostToolBatch, Stop, SubagentStop, SessionEnd.
		"permission": {reg, w, w, ni, ni, wNI, wNI, dn, dn, gone},
		"ask":        {reg, w, w, ni, ni, wNI, wNI, dn, dn, gone},
		// A background subagent: Stop with background_tasks stays working, its
		// agent_id tool events and SubagentStops change nothing, the result
		// comes back as UserPromptSubmit, and only the final Stop is done.
		"subagent": {reg, w, w, w, w, w, w, w, w, w, w, w, w, dn, dn, gone},
		// A background shell: the same shape, without a SubagentStart.
		"background": {reg, w, w, w, w, w, w, w, dn, dn, gone},
		// SessionStart, then /exit (SessionEnd prompt_input_exit).
		"exit": {reg, gone},
	}
	for name, want := range scenarios {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := tmuxtest.New(t)
			pane, pid := fakeClaude(t, s)
			lines := readFixture(t, name)
			if len(lines) != len(want) {
				t.Fatalf("%d fixture events, %d expectations", len(lines), len(want))
			}
			var states []string
			for i, line := range lines {
				hookInProcess(t, s, pane, pid, line)
				got := paneState(s, pane)
				states = append(states, got)
				if got != want[i] {
					t.Errorf("step %d (%s): %s, want %s", i+1, eventName(line), got, want[i])
				}
				if i == 0 && s.PaneOption(pane, state.OptPID) != strconv.Itoa(pid) {
					t.Errorf("pid = %q, want the claude process %d", s.PaneOption(pane, state.OptPID), pid)
				}
			}
			t.Logf("%s: %v", name, states)
			if log, err := os.ReadFile(s.LogPath()); err == nil {
				t.Errorf("hook logged:\n%s", log)
			}
		})
	}
}

// TestHookClaudeSessionStartNeverRaises: SessionStart from any source on a
// pane mid-turn reports done without raising Attention; compaction keeps
// the turn going.
func TestHookClaudeSessionStartNeverRaises(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	pane, pid := fakeClaude(t, s)
	ev := func(js string) { t.Helper(); hookInProcess(t, s, pane, pid, js) }
	ev(`{"hook_event_name":"SessionStart","source":"startup"}`)
	ev(`{"hook_event_name":"UserPromptSubmit","prompt":"x"}`)
	ev(`{"hook_event_name":"SessionStart","source":"compact"}`)
	if got := paneState(s, pane); got != "working/" {
		t.Fatalf("after compact: %s, want working/", got)
	}
	ev(`{"hook_event_name":"SessionStart","source":"resume"}`)
	if got := paneState(s, pane); got != "done/" {
		t.Fatalf("after resume mid-turn: %s, want done/ (no Attention)", got)
	}
	ev(`{"hook_event_name":"UserPromptSubmit","prompt":"x"}`)
	ev(`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	ev(`{"hook_event_name":"SessionStart","source":"clear"}`)
	if got := paneState(s, pane); got != "done/" {
		t.Fatalf("after clear from needs-input: %s, want done/", got)
	}
	// StopFailure ends the turn like Stop.
	ev(`{"hook_event_name":"UserPromptSubmit","prompt":"x"}`)
	ev(`{"hook_event_name":"StopFailure","error":"rate_limit"}`)
	if got := paneState(s, pane); got != "done/done" {
		t.Fatalf("after StopFailure: %s, want done/done", got)
	}
	// Notifications other than the blocking ones change nothing.
	ev(`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	if got := paneState(s, pane); got != "done/done" {
		t.Fatalf("after idle_prompt: %s", got)
	}
}

// hookBinary runs the built binary as `herdmux hook claude` from s.Pane.
func hookBinary(t *testing.T, s *tmuxtest.Server, stdin string, outside bool, extra ...string) tmuxtest.Result {
	t.Helper()
	return s.Run(tmuxtest.Cmd{Args: []string{"hook", "claude"}, Stdin: stdin, Outside: outside, Env: extra})
}

func TestHookClaudeGate(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	start := `{"hook_event_name":"SessionStart","source":"startup"}`
	for _, c := range []struct {
		what    string
		outside bool
		env     []string
	}{
		{"HERDMUX unset", false, nil},
		{"HERDMUX=0", false, []string{"HERDMUX=0"}},
		{"outside tmux", true, []string{"HERDMUX=1"}},
		{"no TMUX_PANE", false, []string{"HERDMUX=1", "TMUX_PANE="}},
	} {
		r := hookBinary(t, s, start, c.outside, c.env...)
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", c.what, r.Code, r.Stdout, r.Stderr)
		}
		if got := s.ListPanes("#{pane_id}:#{" + state.OptAgent + "}"); strings.Join(got, ",") != s.Pane+":" {
			t.Errorf("%s: wrote %v", c.what, got)
		}
	}
	if _, err := os.Stat(s.LogPath()); err == nil {
		log, _ := os.ReadFile(s.LogPath())
		t.Errorf("gated-off hook logged:\n%s", log)
	}
}

func TestHookClaudeMalformedJSON(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	for _, in := range []string{"", "{not json", `{"session_id":"x"}`} {
		r := hookBinary(t, s, in, false, "HERDMUX=1")
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", in, r.Code, r.Stdout, r.Stderr)
		}
	}
	if got := s.PaneOption(s.Pane, state.OptAgent); got != "" {
		t.Errorf("malformed JSON wrote agent %q", got)
	}
	log, _ := os.ReadFile(s.LogPath())
	if n := strings.Count(string(log), "bad hook JSON"); n != 3 {
		t.Errorf("want 3 'bad hook JSON' log lines, got %d:\n%s", n, log)
	}
}

// TestHookClaudeBinaryUnderShell runs the real binary the way claude runs a
// hook: claude (here a bash named claude) → sh -c → herdmux. The pid walk
// finds the claude process, and nothing is printed.
func TestHookClaudeBinaryUnderShell(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	fixture, err := filepath.Abs(filepath.Join(fixtures, "exit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The first line of exit.jsonl is a SessionStart.
	script := `head -n 1 "$1" > "$1.first"; exec -a claude bash -c 'sh -c "herdmux hook claude < \"$0\"; :"; echo "claude=$$"' "$1.first"`
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "events.jsonl")
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script, "bash", copyPath)
	cmd.Env = append(s.Env(s.Pane), "HERDMUX=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	t0 := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %s", err, errb.String())
	}
	t.Logf("hook run (incl. bash/sh startup): %v", time.Since(t0))
	claudePID, ok := strings.CutPrefix(strings.TrimSpace(out.String()), "claude=")
	if !ok || errb.Len() != 0 {
		t.Fatalf("hook printed: stdout=%q stderr=%q", out.String(), errb.String())
	}
	if got := s.PaneOption(s.Pane, state.OptState) + " " + s.PaneOption(s.Pane, state.OptPID); got != "done "+claudePID {
		t.Fatalf("state/pid = %q, want done %s", got, claudePID)
	}
	if log, err := os.ReadFile(s.LogPath()); err == nil {
		t.Errorf("hook logged:\n%s", log)
	}
}
