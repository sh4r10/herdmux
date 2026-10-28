package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sh4r10/herdmux/internal/integrations/claude"
)

// TestHooks: the embedded hooks file parses, covers exactly the events
// claude.Decide acts on for codex, and every entry is a synchronous
// `herdmux hook codex` command. The dev shim must pass the same events.
func TestHooks(t *testing.T) {
	t.Parallel()
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []map[string]any
		} `json:"hooks"`
	}
	if err := json.Unmarshal(Hooks, &s); err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	var top map[string]any
	_ = json.Unmarshal(Hooks, &top)
	if len(top) != 1 {
		t.Errorf("hooks.json must stay hooks-only: %v", top)
	}
	want := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Interrupt", "SessionEnd"}
	if len(s.Hooks) != len(want) {
		t.Errorf("hooks cover %d events, want %d", len(s.Hooks), len(want))
	}
	shim, err := os.ReadFile(filepath.Join("..", "..", "..", "dev", "shims", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range want {
		groups := s.Hooks[ev]
		if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
			t.Errorf("%s: want one group with no matcher and one hook, got %+v", ev, groups)
			continue
		}
		if h := groups[0].Hooks[0]; h["type"] != "command" || h["command"] != Command || h["async"] != nil {
			t.Errorf("%s: hook %v; want a synchronous command %q", ev, h, Command)
		}
		if claude.Decide(claude.Event{Name: ev, Reason: "other"}) == claude.Ignore {
			t.Errorf("%s is hooked but Decide ignores it", ev)
		}
		if !strings.Contains(string(shim), " "+ev+" ") && !strings.Contains(string(shim), " "+ev+";") {
			t.Errorf("dev/shims/codex doesn't pass the %s hook", ev)
		}
	}
	if !strings.Contains(string(shim), `command="`+Command+`"`) || !strings.Contains(string(shim), "mcp_servers.herdmux") ||
		!strings.Contains(string(shim), "features.daemon_auto_start=false") {
		t.Errorf("dev/shims/codex doesn't pass %q, the dev MCP registration and daemon_auto_start=false", Command)
	}
	if Command != "herdmux hook codex" {
		t.Errorf("Command = %q", Command)
	}
}
