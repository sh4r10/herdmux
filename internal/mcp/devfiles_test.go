package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"herdmux/internal/integrations"
	"herdmux/internal/integrations/claude"
)

// The herdmux-dev registrations (dev/claude, dev/opencode, dev/pi), read by
// the shims in dev/shims. Nothing here registers globally.

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func devPath(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "dev"}, parts...)...)
}

// The dev claude settings are the hooks file plus exactly the four read
// pre-allows; the hooks must stay equal to claude.Settings.
func TestDevClaudeSettings(t *testing.T) {
	t.Parallel()
	dev := readJSON(t, devPath("claude", "settings.json"))
	var hooks map[string]any
	if err := json.Unmarshal(claude.Settings, &hooks); err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks["hooks"] == nil {
		t.Fatalf("internal/integrations/claude/settings.json must stay hooks-only: %v", hooks)
	}
	if !reflect.DeepEqual(dev["hooks"], hooks["hooks"]) {
		t.Error("dev/claude/settings.json hooks differ from internal/integrations/claude/settings.json; copy them over")
	}
	want := map[string]any{"permissions": map[string]any{"allow": []any{
		"mcp__herdmux__list_colorschemes", "mcp__herdmux__list_agents",
		"mcp__herdmux__get_colorscheme", "mcp__herdmux__read_config"}}}
	delete(dev, "hooks")
	if !reflect.DeepEqual(dev, want) {
		t.Errorf("dev/claude/settings.json besides hooks = %v, want %v", dev, want)
	}
	var allow []any
	for _, a := range ClaudeAllow() {
		allow = append(allow, a)
	}
	if !reflect.DeepEqual(allow, want["permissions"].(map[string]any)["allow"]) {
		t.Errorf("ClaudeAllow() = %v", allow)
	}
}

func TestDevRegistrations(t *testing.T) {
	t.Parallel()
	cmd := []any{}
	for _, c := range Command {
		cmd = append(cmd, c)
	}
	claudeWant := map[string]any{"mcpServers": map[string]any{Name: map[string]any{
		"type": "stdio", "command": Command[0], "args": cmd[1:], "env": map[string]any{}}}}
	if got := readJSON(t, devPath("claude", "mcp.json")); !reflect.DeepEqual(got, claudeWant) {
		t.Errorf("dev/claude/mcp.json = %v", got)
	}
	ocWant := map[string]any{"mcp": map[string]any{Name: map[string]any{
		"type": "local", "command": cmd, "environment": map[string]any{}, "enabled": true}}}
	if got := readJSON(t, devPath("opencode", "opencode.json")); !reflect.DeepEqual(got, ocWant) {
		t.Errorf("dev/opencode/opencode.json = %v", got)
	}
	pi, err := os.ReadFile(devPath("pi", "herdmux-mcp.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pi), `pi.registerMcpServer("herdmux", { command: "herdmux", args: ["mcp"], env: {}, exposure: "direct" })`) {
		t.Errorf("dev/pi/herdmux-mcp.ts doesn't register herdmux mcp directly:\n%s", pi)
	}
	// MCP is opt-in (install --mcp): the installed integrations never register it.
	if strings.Contains(string(integrations.PiExtension), "registerMcpServer") {
		t.Error("the installed pi extension registers an MCP server")
	}
	if strings.Contains(string(integrations.OpencodePlugin), `"mcp"`) || slices.Contains(strings.Fields(string(claude.Settings)), "mcpServers") {
		t.Error("an installed integration registers MCP")
	}
	// The shims load these files.
	for shim, file := range map[string]string{"claude": "dev/claude/mcp.json", "opencode": "dev/opencode/opencode.json", "pi": "dev/pi/herdmux-mcp.ts"} {
		b, err := os.ReadFile(devPath("shims", shim))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "$root/"+file) {
			t.Errorf("dev/shims/%s doesn't load %s", shim, file)
		}
	}
}
