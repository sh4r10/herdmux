package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSettingsHooks: the embedded hooks file parses, covers the mapped
// events, and every entry is a synchronous `herdmux hook claude` command.
func TestSettingsHooks(t *testing.T) {
	t.Parallel()
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []map[string]any
		} `json:"hooks"`
	}
	if err := json.Unmarshal(Settings, &s); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	want := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest",
		"Notification", "Stop", "StopFailure", "SessionEnd"}
	if len(s.Hooks) != len(want) {
		t.Errorf("hooks cover %d events, want %d", len(s.Hooks), len(want))
	}
	for _, ev := range want {
		groups := s.Hooks[ev]
		if len(groups) == 0 {
			t.Errorf("no hook for %s", ev)
		}
		for _, g := range groups {
			if ev == "Notification" {
				for _, nt := range []string{"permission_prompt", "elicitation_dialog", "elicitation_url_dialog"} {
					if !strings.Contains(g.Matcher, nt) {
						t.Errorf("Notification matcher %q lacks %s", g.Matcher, nt)
					}
				}
				if strings.Contains(g.Matcher, "idle_prompt") {
					t.Error("Notification matcher includes idle_prompt")
				}
			} else if g.Matcher != "" {
				t.Errorf("%s has matcher %q; want every source/tool/reason", ev, g.Matcher)
			}
			if len(g.Hooks) != 1 {
				t.Errorf("%s: %d hooks in a group, want 1", ev, len(g.Hooks))
			}
			for _, h := range g.Hooks {
				if h["type"] != "command" || h["command"] != Command || h["async"] != nil {
					t.Errorf("%s: hook %v; want a synchronous command %q", ev, h, Command)
				}
			}
		}
		if Decide(Event{Name: ev, NotificationType: "permission_prompt", Reason: "other"}) == Ignore {
			t.Errorf("%s is hooked but Decide ignores it", ev)
		}
	}
	if Command != "herdmux hook claude" {
		t.Errorf("Command = %q", Command)
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	bg := []json.RawMessage{json.RawMessage(`{"type":"shell"}`)}
	cases := []struct {
		ev   Event
		want Action
	}{
		{Event{Name: "SessionStart", Source: "startup"}, Register},
		{Event{Name: "SessionStart", Source: "compact"}, Register},
		{Event{Name: "UserPromptSubmit"}, Working},
		{Event{Name: "PreToolUse"}, Working},
		{Event{Name: "PreToolUse", ToolName: "Bash"}, Working},
		{Event{Name: "PreToolUse", ToolName: "request_user_input"}, NeedsInput},
		{Event{Name: "PostToolUse", ToolName: "request_user_input"}, Working},
		{Event{Name: "PostToolUse"}, Working},
		{Event{Name: "PostToolBatch"}, Working},
		{Event{Name: "PreToolUse", AgentID: "a1"}, Ignore},
		{Event{Name: "PostToolUse", AgentID: "a1"}, Ignore},
		{Event{Name: "PermissionRequest"}, NeedsInput},
		{Event{Name: "Notification", NotificationType: "permission_prompt"}, NeedsInput},
		{Event{Name: "Notification", NotificationType: "elicitation_dialog"}, NeedsInput},
		{Event{Name: "Notification", NotificationType: "elicitation_url_dialog"}, NeedsInput},
		{Event{Name: "Notification", NotificationType: "idle_prompt"}, Ignore},
		{Event{Name: "Notification", NotificationType: "agent_completed"}, Ignore},
		{Event{Name: "Stop"}, Done},
		{Event{Name: "Stop", BackgroundTasks: bg}, Ignore},
		{Event{Name: "StopFailure"}, Done},
		{Event{Name: "Interrupt"}, Done},
		{Event{Name: "Interrupt", AgentID: "a1"}, Ignore},
		{Event{Name: "SessionEnd", Reason: "prompt_input_exit"}, Exit},
		{Event{Name: "SessionEnd", Reason: "logout"}, Exit},
		{Event{Name: "SessionEnd", Reason: "other"}, Exit},
		{Event{Name: "SessionEnd", Reason: "clear"}, Ignore},
		{Event{Name: "SessionEnd", Reason: "resume"}, Ignore},
		{Event{Name: "SubagentStart"}, Ignore},
		{Event{Name: "SubagentStop", AgentID: "a1"}, Ignore},
		{Event{Name: "SubagentStop"}, Ignore},
		{Event{Name: "Stop", AgentID: "a1"}, Ignore},
		{Event{Name: "SomethingNew"}, Ignore},
	}
	for _, c := range cases {
		if got := Decide(c.ev); got != c.want {
			t.Errorf("Decide(%+v) = %v, want %v", c.ev, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	ev, err := Parse([]byte(`{"hook_event_name":"StopFailure","error":"rate_limit","background_tasks":null}`))
	if err != nil || ev.Name != "StopFailure" || Decide(ev) != Done {
		t.Fatalf("Parse: %+v %v", ev, err)
	}
	for _, bad := range []string{"", "{", "not json", `{"session_id":"x"}`, `[]`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded", bad)
		}
	}
}

// TestFixturesParse: every recorded fixture line is a hook event.
func TestFixturesParse(t *testing.T) {
	t.Parallel()
	files, _ := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if len(files) != 7 {
		t.Fatalf("want the 7 recorded scenarios, got %v", files)
	}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(nil, 1<<20)
		for n := 1; sc.Scan(); n++ {
			if _, err := Parse(sc.Bytes()); err != nil {
				t.Errorf("%s:%d: %v", f, n, err)
			}
		}
		fh.Close()
	}
}

func TestFindClaudeFakeChain(t *testing.T) {
	t.Parallel()
	type proc struct {
		names []string
		ppid  int
	}
	tree := map[int]proc{
		50: {[]string{"herdmux", "herdmux"}, 40}, // the hook itself
		40: {[]string{"sh", "sh"}, 30},           // claude runs hooks under sh -c
		30: {[]string{"claude", "claude"}, 20},   // claude
		20: {[]string{"bash", "bash"}, 10},       // the pane's shell
		10: {[]string{"tmux: server", "tmux"}, 1},
		// claude started by its versioned path: comm and argv[0] are the version.
		31: {[]string{"2.1.301", "2.1.301"}, 20},
		41: {[]string{"sh", "sh"}, 31},
		// argv[0] only (process.title / exec -a); comm is the real binary.
		32: {[]string{"node", "claude"}, 20},
		42: {[]string{"sh", "sh"}, 32},
		// no claude anywhere.
		43: {[]string{"sh", "sh"}, 20},
		// npm codex: node (the wrapper) runs the native codex, which runs the hook.
		33: {[]string{"node", "node"}, 20},
		34: {[]string{"codex", "codex"}, 33},
		44: {[]string{"sh", "sh"}, 34},
		// a loop must end.
		60: {[]string{"x", "x"}, 61},
		61: {[]string{"y", "y"}, 60},
	}
	info := func(pid int) ([]string, int, bool) {
		p, ok := tree[pid]
		return p.names, p.ppid, ok
	}
	for start, want := range map[int]int{50: 30, 40: 30, 30: 30, 41: 31, 42: 32, 43: 0, 44: 0, 60: 0, 99: 0, 1: 0, 0: 0} {
		if got := find(start, "claude", info); got != want {
			t.Errorf("find(%d, claude) = %d, want %d", start, got, want)
		}
	}
	for start, want := range map[int]int{44: 34, 34: 34, 33: 0, 50: 0} {
		if got := find(start, "codex", info); got != want {
			t.Errorf("find(%d, codex) = %d, want %d", start, got, want)
		}
	}
}

// TestFindClaudeRealChain walks a real process tree: a bash named claude
// (exec -a) running sh -c, running sleep.
func TestFindClaudeRealChain(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("bash", "-c", `exec -a claude bash -c 'sh -c "sleep 30; :"; :'`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	claudePID := cmd.Process.Pid
	// Find the sleep: a grandchild of the claude-named bash.
	var sleepPID int
	deadline := time.Now().Add(5 * time.Second)
	for sleepPID == 0 && time.Now().Before(deadline) {
		for _, sh := range children(claudePID) {
			for _, c := range children(sh) {
				sleepPID = c
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sleepPID == 0 {
		t.Fatal("no sleep grandchild")
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(sleepPID); err == nil {
			_ = p.Kill()
		}
	})
	if got := Find(sleepPID, "claude"); got != claudePID {
		t.Fatalf("Find(sleep %d, claude) = %d, want the claude-named bash %d", sleepPID, got, claudePID)
	}
	if got := Find(sleepPID, "codex"); got != 0 {
		t.Fatalf("Find(sleep %d, codex) = %d, want 0", sleepPID, got)
	}
}

func children(pid int) []int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + strconv.Itoa(pid) + "/children")
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}
