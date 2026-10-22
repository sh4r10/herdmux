package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func names(as []Agent) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Name+"="+a.Key)
	}
	return strings.Join(s, " ")
}

func problems(t *testing.T, err error) string {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want a *config.Error, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "config.toml: ") {
		t.Errorf("error %q lacks the config.toml: prefix", err)
	}
	return err.Error()
}

func getenv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsWithNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Load(getenv(map[string]string{"HERDMUX_CONFIG_DIR": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Agents); got != "claude=c opencode=o pi=p" {
		t.Errorf("agents %q", got)
	}
	if !reflect.DeepEqual(cfg.Keys, DefaultKeys()) {
		t.Errorf("keys %v", cfg.Keys)
	}
	for _, a := range DefaultAgents() {
		if a.Command != a.Name {
			t.Errorf("default %s runs %q", a.Name, a.Command)
		}
	}
}

func TestLoadHonoursConfigDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[[agent]]\nname = \"x\"\ncommand = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(getenv(map[string]string{"HERDMUX_CONFIG_DIR": dir, "HOME": t.TempDir()}))
	if err != nil || names(cfg.Agents) != "x=x" {
		t.Fatalf("%v %v", names(cfg.Agents), err)
	}
	p, _ := Path(getenv(map[string]string{"HOME": "/h"}))
	if p != "/h/.config/herdmux/config.toml" {
		t.Errorf("default path %q", p)
	}
}

func TestOnlyKeysMeansDefaultAgents(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("[keys]\npicker = \"M-a\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Agents); got != "claude=c opencode=o pi=p" {
		t.Errorf("agents %q", got)
	}
}

func TestAgentsReplaceDefaultsInFileOrder(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
[[agent]]
name = "pi"
command = "pi --model x"

[[agent]]
name = "claude"
key = "k"
command = "claude"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Agents); got != "pi=p claude=k" {
		t.Errorf("agents %q", got)
	}
	if cfg.Agents[0].Command != "pi --model x" {
		t.Errorf("command %q", cfg.Agents[0].Command)
	}
	if _, ok := cfg.Agent("opencode"); ok {
		t.Error("opencode kept: the file replaces the defaults")
	}
}

func TestKeyAutoPick(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
[[agent]]
name = "claude"
command = "claude"
[[agent]]
name = "claude-opus"
command = "claude --model opus"
[[agent]]
name = "cool"
key = "o"
command = "cool"
[[agent]]
name = "opencode"
command = "opencode"
`))
	if err != nil {
		t.Fatal(err)
	}
	// Explicit keys are reserved first; q is never picked (it closes the prompt).
	if got := names(cfg.Agents); got != "claude=c claude-opus=l cool=o opencode=p" {
		t.Errorf("agents %q", got)
	}
	cfg, err = Parse([]byte("[[agent]]\nname = \"qq\"\ncommand = \"x\"\n"))
	// Nothing left: the defaults, with the error.
	if got := names(cfg.Agents); got != "claude=c opencode=o pi=p" || err == nil || !strings.Contains(err.Error(), `agent "qq": no free key`) {
		t.Errorf("all-q name: %q %v", got, err)
	}
}

func TestDuplicateKeyFirstWins(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
[[agent]]
name = "one"
key = "x"
command = "one"
[[agent]]
name = "two"
key = "x"
command = "two"
[[agent]]
name = "three"
command = "three"
`))
	msg := problems(t, err)
	if !strings.Contains(msg, `duplicate key "x" on agent "two" (the first one wins)`) {
		t.Errorf("error %q", msg)
	}
	if got := names(cfg.Agents); got != "one=x three=t" {
		t.Errorf("agents %q", got)
	}
}

func TestBrokenEntriesFallBack(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ text, want string }{
		"missing name":    {"[[agent]]\ncommand = \"x\"\n", "agent #1: missing name"},
		"missing command": {"[[agent]]\nname = \"x\"\n", `agent "x": missing command`},
		"empty command":   {"[[agent]]\nname = \"x\"\ncommand = \"  \"\n", `agent "x": missing command`},
		"bad name":        {"[[agent]]\nname = \"a b\"\ncommand = \"x\"\n", `agent "a b": bad name`},
		"long key":        {"[[agent]]\nname = \"x\"\nkey = \"ab\"\ncommand = \"x\"\n", `key "ab" must be one character`},
		"q key":           {"[[agent]]\nname = \"x\"\nkey = \"q\"\ncommand = \"x\"\n", `key "q" is reserved`},
	} {
		cfg, err := Parse([]byte(tc.text))
		msg := problems(t, err)
		if !strings.Contains(msg, tc.want) || !strings.Contains(msg, "using the default agents") {
			t.Errorf("%s: error %q", name, msg)
		}
		if got := names(cfg.Agents); got != "claude=c opencode=o pi=p" {
			t.Errorf("%s: agents %q", name, got)
		}
	}
}

func TestBadTOMLFallsBack(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("[keys]\npicker = \"M-a\"\n\n[[agent]\nname = 1\n"))
	msg := problems(t, err)
	if !strings.Contains(msg, "line ") {
		t.Errorf("error %q has no line", msg)
	}
	if !reflect.DeepEqual(cfg, Defaults()) {
		t.Errorf("not the defaults: %+v", cfg)
	}
	// A wrong type is bad TOML for this file too.
	cfg, err = Parse([]byte("[[agent]]\nname = 5\ncommand = \"x\"\n"))
	problems(t, err)
	if !reflect.DeepEqual(cfg, Defaults()) {
		t.Errorf("wrong type: not the defaults: %+v", cfg)
	}
}

func TestUnknownKeysReported(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("theme = 1\n[[agent]]\nname = \"x\"\ncommand = \"x\"\nenabled = false\n"))
	msg := problems(t, err)
	if !strings.Contains(msg, `unknown key "theme"`) || !strings.Contains(msg, `unknown key "agent.enabled"`) {
		t.Errorf("error %q", msg)
	}
	if names(cfg.Agents) != "x=x" {
		t.Errorf("agents %q", names(cfg.Agents))
	}
}

func TestVisibleHidesCommandsNotOnPath(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	for name, mode := range map[string]os.FileMode{"fake-claude": 0o755, "not-exec": 0o644} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Parse([]byte(`
[[agent]]
name = "a"
command = "fake-claude --x"
[[agent]]
name = "b"
command = "missing-agent"
[[agent]]
name = "c"
command = "not-exec"
[[agent]]
name = "d"
command = "FOO=1 BAR=2 fake-claude"
[[agent]]
name = "e"
command = "` + filepath.Join(bin, "fake-claude") + `"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Visible(t.TempDir() + ":" + bin)); got != "a=a d=d e=e" {
		t.Errorf("visible %q", got)
	}
	if got := names(cfg.Visible("")); got != "e=e" {
		t.Errorf("visible with empty PATH %q", got)
	}
}

func TestKeysMerge(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("[keys]\npicker = \"M-a\"\norganiser = \"\"\ncolorscheme = \"prefix  C\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultKeys()
	want["picker"], want["organiser"], want["colorscheme"] = "M-a", "", "prefix C"
	if !reflect.DeepEqual(cfg.Keys, want) {
		t.Errorf("keys %v", cfg.Keys)
	}
	if s, _ := ParseSpec(cfg.Keys["picker"]); s != (Spec{Table: "root", Key: "M-a"}) {
		t.Errorf("M-a: %+v", s)
	}
	if s, _ := ParseSpec(""); s != (Spec{}) || s.String() != "" {
		t.Errorf(`"": %+v`, s)
	}
}

func TestBadKeysFallBack(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ text, want string }{
		"unknown action": {"[keys]\npick = \"M-a\"\n", `unknown action "pick"`},
		"bad key":        {"[keys]\npicker = \"Ctrl-a\"\n", `"Ctrl-a" is not a tmux key name`},
		"two words":      {"[keys]\npicker = \"root a\"\n", `want "prefix <key>"`},
		"same key twice": {"[keys]\npicker = \"prefix o\"\n", `picker and launch are both "prefix o"`},
	} {
		cfg, err := Parse([]byte(tc.text))
		msg := problems(t, err)
		if !strings.Contains(msg, tc.want) || !strings.Contains(msg, "using the default keys") {
			t.Errorf("%s: error %q", name, msg)
		}
		if !reflect.DeepEqual(cfg.Keys, DefaultKeys()) {
			t.Errorf("%s: keys %v", name, cfg.Keys)
		}
	}
}

func TestValidKey(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"a", "R", "M-a", "C-M-x", "S-Up", "F12", "enter", "Space", "^b", ";", "é", "KP0"} {
		if !validKey(k) {
			t.Errorf("%q rejected", k)
		}
	}
	for _, k := range []string{"", "ab", "Ctrl-a", "M-", "F99", "X-a"} {
		if validKey(k) {
			t.Errorf("%q accepted", k)
		}
	}
}

func TestWriteValidatesFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := getenv(map[string]string{"HERDMUX_CONFIG_DIR": dir})
	good := []byte("[keys]\npicker = \"M-a\"\n")
	if _, err := Write(env, good); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(env, []byte("[keys]\npicker = \"prefix o\"\n")); err == nil {
		t.Fatal("a bad file was accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.toml")); string(b) != string(good) {
		t.Errorf("file changed to %q", b)
	}
}

func TestSessionizer(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`[sessionizer]
roots = ["/tmp", "/home/user/projects"]
depth = 2
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sessionizer.Roots) != 2 {
		t.Errorf("roots %v", cfg.Sessionizer.Roots)
	}
	if cfg.Sessionizer.Depth != 2 {
		t.Errorf("depth %d", cfg.Sessionizer.Depth)
	}

	// Bad depth is ignored; uses default.
	cfg, _ = Parse([]byte(`[sessionizer]
roots = ["/tmp"]
depth = -1
`))
	if cfg.Sessionizer.Depth != DefaultSessionizer().Depth {
		t.Errorf("bad depth fallback %d", cfg.Sessionizer.Depth)
	}

	// No roots: fallback to defaults.
	cfg, _ = Parse([]byte(`[sessionizer]
`))
	if len(cfg.Sessionizer.Roots) != 0 || cfg.Sessionizer.Depth == 0 {
		t.Errorf("empty sessionizer %v/%d", cfg.Sessionizer.Roots, cfg.Sessionizer.Depth)
	}
}
