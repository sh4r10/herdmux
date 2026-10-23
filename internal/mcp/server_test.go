package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// env is a getenv for the server: inside s (TMUX, TMUX_PANE = pane) unless
// pane is "", which means outside tmux.
func env(s *tmuxtest.Server, pane string) func(string) string {
	m := map[string]string{"HOME": s.Home, "HERDMUX_CONFIG_DIR": s.ConfigDir, "HERDMUX": "1"}
	if pane != "" {
		m["TMUX"] = s.SocketPath + "," + s.PID + ",0"
		m["TMUX_PANE"] = pane
	}
	return func(k string) string { return m[k] }
}

// connect runs the server in-process and returns a connected client.
func connect(t *testing.T, getenv func(string) string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := Server(getenv).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Wait() })
	return cs
}

// call returns the tool's text and whether it is an error result.
func call(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out []string
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return strings.Join(out, "\n"), r.IsError
}

func mustCall(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	out, isErr := call(t, cs, tool, args)
	if isErr {
		t.Fatalf("%s(%v) failed: %s", tool, args, out)
	}
	return out
}

func mustFail(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any, want string) {
	t.Helper()
	out, isErr := call(t, cs, tool, args)
	if !isErr || !strings.Contains(out, want) {
		t.Fatalf("%s(%v) = %q (error %v), want an error containing %q", tool, args, out, isErr, want)
	}
}

func TestToolsAndAnnotations(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	cs := connect(t, env(s, s.Pane))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		ro := slices.Contains(ReadOnly, tool.Name)
		if a.ReadOnlyHint != ro {
			t.Errorf("%s: readOnlyHint %v, want %v", tool.Name, a.ReadOnlyHint, ro)
		}
		if !ro {
			wantD := tool.Name != SetColorscheme
			if a.DestructiveHint == nil || *a.DestructiveHint != wantD {
				t.Errorf("%s: destructiveHint %v, want %v", tool.Name, a.DestructiveHint, wantD)
			}
		}
	}
	slices.Sort(names)
	want := []string{"delete_colorscheme", "get_colorscheme", "list_agents", "list_colorschemes",
		"read_config", "set_colorscheme", "write_colorscheme", "write_config"}
	if !slices.Equal(names, want) {
		t.Errorf("tools %v, want %v", names, want)
	}
}

func TestWriteConfig(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	cs := connect(t, env(s, s.Pane))
	path := filepath.Join(s.ConfigDir, "config.toml")

	if out := mustCall(t, cs, ReadConfig, nil); out != "" {
		t.Errorf("read_config with no file = %q", out)
	}
	good := "[keys]\npicker = \"prefix a\"\n"
	mustCall(t, cs, WriteConfig, map[string]any{"text": good})

	for _, bad := range []string{
		"[keys\npicker = 1\n",                               // bad TOML
		"[keys]\npicker = \"prefix a\"\npicker = \"M-a\"\n", // duplicate key
		"[[agents]]\nname = \"x\"\n",                        // unknown key
	} {
		mustFail(t, cs, WriteConfig, map[string]any{"text": bad}, "config.toml: ")
		if b, _ := os.ReadFile(path); string(b) != good {
			t.Errorf("after a rejected write the file is %q", b)
		}
	}
	if out := mustCall(t, cs, ReadConfig, nil); out != good {
		t.Errorf("read_config = %q", out)
	}

	// A changed [keys] entry is rebound live.
	moved := "[keys]\npicker = \"prefix p\"\n"
	if out := mustCall(t, cs, WriteConfig, map[string]any{"text": moved}); !strings.Contains(out, "rebound") {
		t.Errorf("write_config = %q", out)
	}
	if got, _ := s.Client.KeyBinding("prefix", "p"); !strings.Contains(got, "herdmux picker") {
		t.Errorf("prefix p = %q, want the picker", got)
	}
	if b, _ := os.ReadFile(path); string(b) != moved {
		t.Errorf("file %q", b)
	}
}

func TestSetColorscheme(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	cs := connect(t, env(s, s.Pane))
	mustCall(t, cs, SetColorscheme, map[string]any{"name": "tokyonight-night"})
	if got := s.Tmux("show-options", "-gqv", colorscheme.OptActive); got != "tokyonight-night" {
		t.Errorf("active %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ConfigDir, "colorscheme")); strings.TrimSpace(string(b)) != "tokyonight-night" {
		t.Errorf("saved %q", b)
	}
	mustFail(t, cs, SetColorscheme, map[string]any{"name": "nope"}, "no such colorscheme")

	var list []SchemeInfo
	if err := json.Unmarshal([]byte(mustCall(t, cs, ListColorschemes, nil)), &list); err != nil {
		t.Fatal(err)
	}
	for _, i := range list {
		if on := i.Name == "tokyonight-night"; i.Active != on || i.Saved != on {
			t.Errorf("%s: active %v saved %v", i.Name, i.Active, i.Saved)
		}
	}
}

func schemes(t *testing.T, cs *sdk.ClientSession) map[string]SchemeInfo {
	t.Helper()
	var list []SchemeInfo
	if err := json.Unmarshal([]byte(mustCall(t, cs, ListColorschemes, nil)), &list); err != nil {
		t.Fatal(err)
	}
	m := map[string]SchemeInfo{}
	for _, i := range list {
		m[i.Name] = i
	}
	return m
}

func TestWriteAndDeleteColorscheme(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	cs := connect(t, env(s, s.Pane))
	mustCall(t, cs, SetColorscheme, map[string]any{"name": "flexoki-dark"})
	raw := mustCall(t, cs, GetColorscheme, map[string]any{"name": "flexoki-dark"})
	if !strings.Contains(raw, `accent                = "#ce5d97"`) {
		t.Fatalf("get_colorscheme:\n%s", raw)
	}

	bad := strings.Replace(raw, `"#ce5d97"`, `"#12345"`, 1)
	mustFail(t, cs, WriteColorscheme, map[string]any{"name": "flexoki-dark", "text": bad}, "is not #rrggbb")
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "colors", "flexoki-dark.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rejected scheme was written: %v", err)
	}

	// A shadow of the active scheme is reported as override and applied live.
	shadow := strings.ReplaceAll(raw, `"#ce5d97"`, `"#00ff00"`)
	mustCall(t, cs, WriteColorscheme, map[string]any{"name": "flexoki-dark", "text": shadow})
	if got := schemes(t, cs)["flexoki-dark"]; got.Source != "override" || !got.Valid || !got.Active {
		t.Errorf("after the shadow write: %+v", got)
	}
	if got := s.Tmux("show-options", "-gqv", colorscheme.Option("accent")); got != "#00ff00" {
		t.Errorf("accent %q, want the shadow's", got)
	}
	if out := mustCall(t, cs, GetColorscheme, map[string]any{"name": "flexoki-dark"}); out != shadow {
		t.Errorf("get_colorscheme returns the bundled text after a shadow write")
	}

	// A user-only scheme that isn't in use is saved without a re-apply.
	mustCall(t, cs, WriteColorscheme, map[string]any{"name": "mine", "text": raw})
	if got := schemes(t, cs)["mine"]; got.Source != "user" || got.Family != "user" {
		t.Errorf("mine: %+v", got)
	}

	// Deleting the shadow brings the bundled scheme back, live.
	mustCall(t, cs, DeleteColorscheme, map[string]any{"name": "flexoki-dark"})
	if got := schemes(t, cs)["flexoki-dark"]; got.Source != "bundled" {
		t.Errorf("after delete: %+v", got)
	}
	if got := s.Tmux("show-options", "-gqv", colorscheme.Option("accent")); got != "#ce5d97" {
		t.Errorf("accent %q, want the bundled one", got)
	}
	mustFail(t, cs, DeleteColorscheme, map[string]any{"name": "tokyonight-night"}, "bundled")
	mustFail(t, cs, DeleteColorscheme, map[string]any{"name": "flexoki-dark"}, "bundled")
	mustFail(t, cs, DeleteColorscheme, map[string]any{"name": "nope"}, "no such colorscheme")
	mustCall(t, cs, DeleteColorscheme, map[string]any{"name": "mine"})
}

func TestListAgentsMarksCaller(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	a := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "exec bash -c 'exec -a claude sleep 100000'")
	b := s.Tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "exec bash -c 'exec -a pi sleep 100000'")
	s.Eventually(5e9, "agent processes", func() bool {
		return s.Display(a, "#{pane_current_command}") == "claude" && s.Display(b, "#{pane_current_command}") == "pi"
	})
	cs := connect(t, env(s, b))
	var agents []map[string]any
	if err := json.Unmarshal([]byte(mustCall(t, cs, ListAgents, nil)), &agents); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ag := range agents {
		got[ag["pane"].(string)] = ag["caller"].(bool)
		for _, k := range []string{"session", "window", "window_name", "agent", "state", "attention"} {
			if _, ok := ag[k]; !ok {
				t.Errorf("agent %v has no %q", ag, k)
			}
		}
	}
	if len(got) != 2 || got[a] || !got[b] {
		t.Errorf("agents %v: want %s (not caller) and %s (caller)", agents, a, b)
	}
}

func TestOutsideTmux(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	cs := connect(t, env(s, ""))
	mustFail(t, cs, SetColorscheme, map[string]any{"name": "tokyonight-night"}, "not inside tmux")
	mustFail(t, cs, ListAgents, nil, "not inside tmux")
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "colorscheme")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("set_colorscheme outside tmux saved: %v", err)
	}

	mustCall(t, cs, WriteConfig, map[string]any{"text": "[keys]\npicker = \"prefix p\"\n"})
	if out := mustCall(t, cs, ReadConfig, nil); !strings.Contains(out, "prefix p") {
		t.Errorf("read_config = %q", out)
	}
	raw := mustCall(t, cs, GetColorscheme, map[string]any{"name": "gruvbox-dark"})
	mustCall(t, cs, WriteColorscheme, map[string]any{"name": "mine", "text": raw})
	if got := schemes(t, cs); got["mine"].Source != "user" || !got["flexoki-dark"].Active {
		t.Errorf("list outside tmux: mine %+v, flexoki-dark %+v", got["mine"], got["flexoki-dark"])
	}
	mustCall(t, cs, DeleteColorscheme, map[string]any{"name": "mine"})
	if got, _ := s.Client.KeyBinding("prefix", "p"); strings.Contains(got, "herdmux") {
		t.Errorf("outside tmux, the server was rebound: %q", got)
	}
}

func TestServeNeedsHERDMUX(t *testing.T) {
	t.Parallel()
	getenv := func(string) string { return "" }
	if err := Serve(context.Background(), getenv, strings.NewReader(""), &strings.Builder{}); !errors.Is(err, ErrNotEnabled) {
		t.Errorf("Serve without HERDMUX=1: %v", err)
	}
}

// The real binary, over stdio: refuses without HERDMUX=1, serves with it.
func TestBinaryStdio(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	r := s.Run(tmuxtest.Cmd{Args: []string{"mcp"}})
	if r.Code != 1 || !strings.Contains(r.Stderr, "HERDMUX=1 is not set") {
		t.Errorf("mcp without HERDMUX=1: code %d, stderr %q", r.Code, r.Stderr)
	}

	cmd := exec.Command(tmuxtest.Binary(t), "mcp")
	cmd.Env = append(s.Env(s.Pane), "HERDMUX=1")
	ctx := context.Background()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if got := cs.InitializeResult().ServerInfo.Name; got != Name {
		t.Errorf("server name %q", got)
	}
	mustCall(t, cs, SetColorscheme, map[string]any{"name": "gruvbox-dark"})
	if got := s.Tmux("show-options", "-gqv", colorscheme.OptActive); got != "gruvbox-dark" {
		t.Errorf("active %q", got)
	}
}
