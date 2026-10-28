package install

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sh4r10/herdmux/internal/integrations"
	"github.com/sh4r10/herdmux/internal/integrations/claude"
	"github.com/sh4r10/herdmux/internal/integrations/codex"
	"github.com/sh4r10/herdmux/internal/mcp"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// Every test here runs against a temp HOME (and XDG_CONFIG_HOME), with fake
// herdmux/claude/pi executables on a temp PATH and either no tmux server or
// a tmuxtest one. guard() makes any slip that reaches the real home fail.

type fakeHome struct {
	t    *testing.T
	home string
	bin  string
	log  string // the fake agent CLIs' argv log (outside HOME)
	env  map[string]string
	tmux func() (*tmux.Client, error)
}

const fakeClaude = `#!/bin/sh
{ printf 'claude'; for a in "$@"; do printf ' [%s]' "$a"; done; printf '\n'; } >> LOG
case "$1 $2" in
"mcp add") /bin/mkdir -p "$HOME" && printf '{"mcpServers":{"herdmux":{"type":"stdio","command":"herdmux","args":["mcp"],"env":{}}}}\n' > "$HOME/.claude.json" ;;
"mcp remove") printf '{"mcpServers":{}}\n' > "$HOME/.claude.json" ;;
esac
`

const fakeCodex = `#!/bin/sh
{ printf 'codex'; for a in "$@"; do printf ' [%s]' "$a"; done; printf '\n'; } >> LOG
case "$1 $2" in
"mcp add") /bin/mkdir -p "$HOME/.codex" && printf '[mcp_servers.herdmux]\ncommand = "herdmux"\nargs = ["mcp"]\n' >> "$HOME/.codex/config.toml" ;;
"mcp remove") printf 'model = "gpt-5"\n' > "$HOME/.codex/config.toml" ;;
"features disable") printf '[features]\ndaemon_auto_start = false\n' >> "$HOME/.codex/config.toml" ;;
"features enable") /bin/sed -i '/^\[features\]$/d;/daemon_auto_start/d' "$HOME/.codex/config.toml" ;;
esac
`

const fakePi = `#!/bin/sh
{ printf 'pi'; for a in "$@"; do printf ' [%s]' "$a"; done; printf '\n'; } >> LOG
case "$1 $2" in
"mcp add") /bin/mkdir -p "$HOME/.pi/agent" && printf '{"mcpServers":{"herdmux":{"command":"herdmux","args":["mcp"],"exposure":"direct"}}}\n' > "$HOME/.pi/agent/mcp.json" ;;
"mcp remove") printf '{"mcpServers":{}}\n' > "$HOME/.pi/agent/mcp.json" ;;
esac
`

func newHome(t *testing.T) *fakeHome {
	t.Helper()
	root := t.TempDir()
	h := &fakeHome{t: t, home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin"), log: filepath.Join(root, "agent-cli.log")}
	for _, d := range []string{h.home, h.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.writeBin("herdmux", "#!/bin/sh\nexit 0\n")
	h.writeBin("claude", strings.ReplaceAll(fakeClaude, "LOG", h.log))
	h.writeBin("codex", strings.ReplaceAll(fakeCodex, "LOG", h.log))
	h.writeBin("pi", strings.ReplaceAll(fakePi, "LOG", h.log))
	h.env = map[string]string{"HOME": h.home, "XDG_CONFIG_HOME": filepath.Join(h.home, ".config"), "PATH": h.bin}
	h.tmux = noServer(t)
	return h
}

// noServer is a test socket with no server behind it.
func noServer(t *testing.T) func() (*tmux.Client, error) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := tmux.TestSocketPrefix + "none-" + hex.EncodeToString(b[:])
	return func() (*tmux.Client, error) { return tmux.OnSocket(name) }
}

func (h *fakeHome) writeBin(name, body string) {
	if err := os.WriteFile(filepath.Join(h.bin, name), []byte(body), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHome) path(rel string) string { return filepath.Join(h.home, rel) }

func (h *fakeHome) write(rel, body string) {
	h.t.Helper()
	p := h.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHome) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(h.path(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *fakeHome) opts() Options {
	return Options{Getenv: func(k string) string { return h.env[k] }, Out: &bytes.Buffer{}, Tmux: h.tmux}
}

func (h *fakeHome) install(mod func(*Options)) (string, error) {
	o := h.opts()
	if mod != nil {
		mod(&o)
	}
	err := Install(o)
	return o.Out.(*bytes.Buffer).String(), err
}

func (h *fakeHome) mustInstall(mod func(*Options)) string {
	h.t.Helper()
	out, err := h.install(mod)
	if err != nil {
		h.t.Fatalf("install: %v\n%s", err, out)
	}
	return out
}

func (h *fakeHome) uninstall(purge bool) string {
	h.t.Helper()
	o := h.opts()
	o.Purge = purge
	if err := Uninstall(o); err != nil {
		h.t.Fatalf("uninstall: %v\n%s", err, o.Out)
	}
	return o.Out.(*bytes.Buffer).String()
}

// tree maps every file (and dir, as "<dir>/") under HOME to its content.
func (h *fakeHome) tree() map[string]string {
	h.t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(h.home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(h.home, p)
		if d.IsDir() {
			m[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		m[rel] = string(b)
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func diffTrees(t *testing.T, before, after map[string]string, ignore func(string) bool) {
	t.Helper()
	var keys []string
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ignore != nil && ignore(k) {
			continue
		}
		b, inB := before[k]
		a, inA := after[k]
		switch {
		case !inA:
			t.Errorf("%s was removed", k)
		case !inB:
			t.Errorf("%s was added", k)
		case a != b:
			t.Errorf("%s changed:\n%s", k, unifiedDiff("before", "after", []byte(b), []byte(a)))
		}
	}
}

const (
	seedTmuxConf = `set -g mouse on
set -g @plugin 'tmux-plugins/tpm'
set -g @plugin 'tmux-plugins/tmux-sensible'
set -g @plugin 'janoamaral/tokyo-night-tmux'
run '~/.tmux/plugins/tpm/tpm'
`
	seedSettings = `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "notify-send done"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "audit-bash"
          }
        ]
      }
    ]
  },
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ]
  },
  "zeta": 1.50
}
`
	seedCodexHooks = `{
  "description": "mine",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "notify-send done"
          }
        ]
      }
    ]
  }
}
`
	seedOpencode = `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "other": {
      "type": "local",
      "command": [
        "other-mcp"
      ]
    }
  },
  "theme": "system"
}
`
	seedTUI       = "// herdr-managed\n{\n  \"plugin\": [\"herdr-tui-session.js\"], // keep\n}\n"
	seedHerdrPlug = "// HERDR plugin\nexport default {}\n"
)

// seed writes a realistic home: tmux.conf with TPM, settings.json with
// other hooks, herdr's opencode plugin and tui.jsonc, an opencode.json with
// another MCP server, and herdmux's own config.
func (h *fakeHome) seed() {
	h.write(".config/tmux/tmux.conf", seedTmuxConf)
	h.write(".claude/settings.json", seedSettings)
	h.write(".codex/hooks.json", seedCodexHooks)
	h.write(".codex/config.toml", "model = \"gpt-5\"\n")
	h.write(".config/opencode/plugins/herdr-agent-state.js", seedHerdrPlug)
	h.write(".config/opencode/tui.jsonc", seedTUI)
	h.write(".config/opencode/opencode.json", seedOpencode)
	h.write(".config/herdmux/config.toml", "[keys]\npicker = \"M-a\"\n")
	h.write(".config/herdmux/colorscheme", "gruvbox-dark\n")
}

func TestPreflightListsEveryProblemAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	h.write(".claude/settings.json", "{ not json")
	if err := os.Remove(filepath.Join(h.bin, "herdmux")); err != nil {
		t.Fatal(err)
	}
	// ~/.pi exists but can't be written, so extensions/ can't be created.
	if err := os.MkdirAll(h.path(".pi"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.path(".pi"), 0o755) })
	before := h.tree()
	out, err := h.install(func(o *Options) { o.MCP = true })
	if err == nil {
		t.Fatalf("install succeeded:\n%s", out)
	}
	for _, want := range []string{"refusing to install; nothing was written", "herdmux is not on PATH", "settings.json doesn't parse", "~/.pi is not writable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	diffTrees(t, before, h.tree(), nil)
	if _, err := os.Stat(h.log); err == nil {
		t.Error("an agent CLI ran despite the refusal")
	}
}

func TestPreflightRefusesWhenTmuxPATHLacksHerdmux(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	h := newHome(t)
	h.seed()
	h.tmux = func() (*tmux.Client, error) { return s.Client, nil }
	s.Tmux("set-environment", "-g", "PATH", "/usr/bin:/bin")
	before := h.tree()
	_, err := h.install(nil)
	if err == nil || !strings.Contains(err.Error(), "not on the running tmux server's global PATH (/usr/bin:/bin)") {
		t.Fatalf("err = %v", err)
	}
	diffTrees(t, before, h.tree(), nil)

	// With herdmux on tmux's global PATH, it installs.
	s.Tmux("set-environment", "-g", "PATH", h.bin+":/usr/bin")
	h.mustInstall(nil)
}

func TestFreshInstall(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	out := h.mustInstall(nil)

	conf := h.read(".config/tmux/tmux.conf")
	if conf != seedTmuxConf+Block {
		t.Errorf("tmux.conf =\n%s", conf)
	}
	if strings.Index(conf, Block) < strings.Index(conf, "run '~/.tmux/plugins/tpm/tpm'") {
		t.Error("the block is before the TPM line")
	}
	if !strings.Contains(out, "warning: ~/.config/tmux/tmux.conf:4 still loads janoamaral/tokyo-night-tmux") {
		t.Errorf("no tokyo-night warning:\n%s", out)
	}
	if !strings.Contains(out, "tmux source-file ~/.config/tmux/tmux.conf") {
		t.Errorf("no reload advice:\n%s", out)
	}

	// Hooks merged next to the user's, key order kept.
	settings := h.read(".claude/settings.json")
	d, err := parseDoc([]byte(settings))
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(d.root); got != "model hooks permissions zeta" {
		t.Errorf("top-level keys = %s", got)
	}
	hooks, _ := d.root.get("hooks")
	if got := keys(hooks.(*object)); got != "Stop PreToolUse SessionStart UserPromptSubmit PostToolUse PermissionRequest Notification StopFailure SessionEnd" {
		t.Errorf("hook events = %s", got)
	}
	stop, _ := hooks.(*object).get("Stop")
	if c := canonical(stop); !strings.Contains(c, "notify-send done") || strings.Index(c, "notify-send") > strings.Index(c, claude.Command) {
		t.Errorf("Stop = %s, want the user's hook then ours", c)
	}
	if !strings.Contains(settings, `"zeta": 1.50`) || strings.Contains(settings, "mcp__herdmux") {
		t.Errorf("settings.json:\n%s", settings)
	}
	if !strings.Contains(out, "open codex and run /hooks") {
		t.Errorf("no codex trust advice:\n%s", out)
	}
	ch := h.read(".codex/hooks.json")
	cd, err := parseDoc([]byte(ch))
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(cd.root); got != "description hooks" {
		t.Errorf("hooks.json top-level keys = %s", got)
	}
	chooks, _ := cd.root.get("hooks")
	if got := keys(chooks.(*object)); got != "Stop SessionStart UserPromptSubmit PreToolUse PostToolUse PermissionRequest Interrupt SessionEnd" {
		t.Errorf("codex hook events = %s", got)
	}
	stop, _ = chooks.(*object).get("Stop")
	if c := canonical(stop); !strings.Contains(c, "notify-send done") || strings.Index(c, "notify-send") > strings.Index(c, codex.Command) {
		t.Errorf("codex Stop = %s, want the user's hook then ours", c)
	}
	if strings.Contains(ch, claude.Command) || h.read(".codex/config.toml") != "model = \"gpt-5\"\n[features]\ndaemon_auto_start = false\n" {
		t.Errorf("codex files:\n%s\n%s", ch, h.read(".codex/config.toml"))
	}
	if !strings.Contains(out, "background server is off") {
		t.Errorf("no codex daemon advice:\n%s", out)
	}

	// Plugin files with their headers.
	for rel, want := range map[string][]byte{
		".config/opencode/plugins/herdmux-agent-state.js": integrations.OpencodePlugin,
		".pi/agent/extensions/herdmux-agent-state.ts":     integrations.PiExtension,
	} {
		got := h.read(rel)
		if got != string(want) {
			t.Errorf("%s isn't the embedded file", rel)
		}
		if id, v := Header([]byte(got)); id == "" || v < 0 {
			t.Errorf("%s header: id=%q version=%d", rel, id, v)
		}
	}
	if h.read(".config/opencode/tui.jsonc") != seedTUI || h.read(".config/opencode/plugins/herdr-agent-state.js") != seedHerdrPlug {
		t.Error("herdr's files changed")
	}
	if h.read(".config/opencode/opencode.json") != seedOpencode {
		t.Error("opencode.json changed without --mcp")
	}

	// Backups: once, of the edited files only, never overwritten.
	if h.read(".config/tmux/tmux.conf.herdmux.bak") != seedTmuxConf || h.read(".claude/settings.json.herdmux.bak") != seedSettings ||
		h.read(".codex/hooks.json.herdmux.bak") != seedCodexHooks {
		t.Error("backups don't hold the originals")
	}
	h.write(".config/tmux/tmux.conf", conf+"set -g status-position top\n") // block no longer last
	out = h.mustInstall(nil)
	if !strings.Contains(out, "moved the herdmux block to the end") {
		t.Errorf("out:\n%s", out)
	}
	if got := h.read(".config/tmux/tmux.conf"); got != seedTmuxConf+"set -g status-position top\n"+Block {
		t.Errorf("tmux.conf after the move =\n%s", got)
	}
	if h.read(".config/tmux/tmux.conf.herdmux.bak") != seedTmuxConf {
		t.Error("the backup was overwritten")
	}

	// Atomic writes leave no temp files.
	for k := range h.tree() {
		if strings.Contains(k, "herdmux-tmp") {
			t.Errorf("temp file left behind: %s", k)
		}
	}
}

func keys(o *object) string {
	var ks []string
	for _, m := range o.members {
		ks = append(ks, m.key)
	}
	return strings.Join(ks, " ")
}

func TestInstallIntoEmptyHome(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.mustInstall(nil)
	if got := h.read(".config/tmux/tmux.conf"); got != Block {
		t.Errorf("tmux.conf = %q", got)
	}
	if _, err := parseDoc([]byte(h.read(".claude/settings.json"))); err != nil {
		t.Error(err)
	}
	h.uninstall(false)
	// Files install created are removed again; dirs it made may stay.
	for k := range h.tree() {
		if !strings.HasSuffix(k, "/") {
			t.Errorf("left %s", k)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	h.mustInstall(func(o *Options) { o.MCP = true })
	first := h.tree()
	log1, _ := os.ReadFile(h.log)
	out := h.mustInstall(func(o *Options) { o.MCP = true })
	diffTrees(t, first, h.tree(), nil)
	if log2, _ := os.ReadFile(h.log); !bytes.Equal(log1, log2) {
		t.Errorf("the second install ran agent CLIs again:\n%s", log2)
	}
	if !strings.Contains(out, "already registered") || !strings.Contains(out, "up to date") {
		t.Errorf("second run:\n%s", out)
	}
}

func TestDryRunShowsDiffsAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	before := h.tree()
	out := h.mustInstall(func(o *Options) { o.DryRun = true; o.MCP = true })
	diffTrees(t, before, h.tree(), nil)
	for _, want := range []string{
		"--- " + h.path(".config/tmux/tmux.conf"),
		"+# >>> herdmux >>>\n+run-shell 'herdmux init'\n+# <<< herdmux <<<\n",
		"+            \"command\": \"herdmux hook claude\"",
		"--- /dev/null\n+++ " + h.path(".pi/agent/extensions/herdmux-agent-state.ts"),
		"+    \"herdmux\": {",
		"would run: claude mcp add -s user herdmux -- herdmux mcp",
		"would run: pi mcp add herdmux --exposure direct -- herdmux mcp",
		"would save the original as ~/.claude/settings.json.herdmux.bak",
		"nothing was written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(h.log); err == nil {
		t.Error("--dry-run ran an agent CLI")
	}
}

func TestUpgradeStalePlugin(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.mustInstall(nil)
	_, v := Header(integrations.PiExtension)
	old := regexp.MustCompile(`(?m)^// HERDMUX_INTEGRATION_VERSION: \d+$`).ReplaceAllString(string(integrations.PiExtension), "// HERDMUX_INTEGRATION_VERSION: 0") + "// old\n"
	h.write(".pi/agent/extensions/herdmux-agent-state.ts", old)
	before := h.tree()
	out := h.mustInstall(func(o *Options) { o.DryRun = true })
	diffTrees(t, before, h.tree(), nil)
	if want := "pi extension ~/.pi/agent/extensions/herdmux-agent-state.ts (stale: version 0 on disk, " + strconv.Itoa(v) + " in this herdmux): would upgrade it"; !strings.Contains(out, want) {
		t.Errorf("dry run lacks %q:\n%s", want, out)
	}
	if !strings.Contains(out, "opencode plugin ~/.config/opencode/plugins/herdmux-agent-state.js: up to date") {
		t.Errorf("dry run:\n%s", out)
	}
	h.mustInstall(nil)
	if h.read(".pi/agent/extensions/herdmux-agent-state.ts") != string(integrations.PiExtension) {
		t.Error("install didn't overwrite the stale extension")
	}
}

func TestRefusesForeignFileAtPluginPath(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.write(".config/opencode/plugins/herdmux-agent-state.js", "// someone else's\n")
	_, err := h.install(nil)
	if err == nil || !strings.Contains(err.Error(), "exists and isn't herdmux's") {
		t.Fatalf("err = %v", err)
	}
}

func TestMCPRegistrations(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	h.mustInstall(func(o *Options) { o.MCP = true })
	log, _ := os.ReadFile(h.log)
	if want := "codex [features] [disable] [daemon_auto_start]\nclaude [mcp] [add] [-s] [user] [herdmux] [--] [herdmux] [mcp]\ncodex [mcp] [add] [herdmux] [--] [herdmux] [mcp]\npi [mcp] [add] [herdmux] [--exposure] [direct] [--] [herdmux] [mcp]\n"; string(log) != want {
		t.Errorf("agent CLI calls:\n%s\nwant:\n%s", log, want)
	}
	oc := h.read(".config/opencode/opencode.json")
	d, err := parseDoc([]byte(oc))
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(d.root); got != "$schema mcp theme" {
		t.Errorf("opencode.json keys = %s", got)
	}
	m, _ := d.root.get("mcp")
	if got := keys(m.(*object)); got != "other herdmux" {
		t.Errorf("mcp keys = %s", got)
	}
	e, _ := m.(*object).get("herdmux")
	if got := canonical(e); got != canonical(opencodeEntry()) || !strings.Contains(oc, `"type": "local"`) {
		t.Errorf("mcp.herdmux = %s", got)
	}
	settings := h.read(".claude/settings.json")
	sd, _ := parseDoc([]byte(settings))
	perms, _ := sd.root.get("permissions")
	allow, _ := perms.(*object).get("allow")
	want := []any{"Bash(ls:*)"}
	for _, a := range mcp.ClaudeAllow() {
		want = append(want, a)
	}
	if canonical(allow) != canonical(want) {
		t.Errorf("permissions.allow = %s", canonical(allow))
	}

	h.uninstall(false)
	log, _ = os.ReadFile(h.log)
	if !strings.HasSuffix(string(log), "codex [features] [enable] [daemon_auto_start]\nclaude [mcp] [remove] [-s] [user] [herdmux]\ncodex [mcp] [remove] [herdmux]\npi [mcp] [remove] [herdmux]\n") {
		t.Errorf("uninstall's agent CLI calls:\n%s", log)
	}
	if h.read(".config/opencode/opencode.json") != seedOpencode || h.read(".claude/settings.json") != seedSettings || h.read(".codex/hooks.json") != seedCodexHooks {
		t.Error("uninstall didn't restore opencode.json, settings.json and hooks.json")
	}
	// Re-running install registers nothing twice.
	h.mustInstall(func(o *Options) { o.MCP = true })
	h.mustInstall(func(o *Options) { o.MCP = true })
	log, _ = os.ReadFile(h.log)
	if n := strings.Count(string(log), "[add]"); n != 6 {
		t.Errorf("want 6 mcp add calls over three installs, got %d:\n%s", n, log)
	}
	if n := strings.Count(string(log), "[features] [disable]"); n != 2 {
		t.Errorf("want 2 features disable calls (one per install after an uninstall), got %d:\n%s", n, log)
	}
}

func TestMidwayFailureReportsDoneSteps(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	h.writeBin("claude", strings.ReplaceAll(strings.Replace(fakeClaude, "case ", "echo boom >&2; exit 1\ncase ", 1), "LOG", h.log))
	out, err := h.install(func(o *Options) { o.MCP = true })
	if err == nil {
		t.Fatalf("install succeeded:\n%s", out)
	}
	for _, want := range []string{"claude MCP: claude mcp add -s user herdmux -- herdmux mcp: exit status 1: boom",
		"done before it: tmux.conf; claude hooks; codex hooks; codex daemon; opencode plugin; pi extension; opencode MCP", "run the command again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	// Re-running after the fix finishes the job.
	h.writeBin("claude", strings.ReplaceAll(fakeClaude, "LOG", h.log))
	h.mustInstall(func(o *Options) { o.MCP = true })
}

// The Done-when round trip: install then uninstall restores every file to
// its original bytes (and key order), removes the plugin files and leaves
// only the backups.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, withMCP := range []bool{false, true} {
		h := newHome(t)
		h.seed()
		before := h.tree()
		h.mustInstall(func(o *Options) { o.MCP = withMCP })
		out := h.uninstall(false)
		after := h.tree()
		diffTrees(t, before, after, func(k string) bool {
			// Backups are expected; the agent CLIs' own files and the dirs
			// install made for the plugins may stay.
			return strings.HasSuffix(k, bakSuffix) || k == ".claude.json" || k == ".pi/agent/mcp.json" || k == ".codex/config.toml" ||
				(strings.HasSuffix(k, "/") && strings.HasPrefix(k, ".pi/"))
		})
		for _, k := range []string{".config/tmux/tmux.conf.herdmux.bak", ".claude/settings.json.herdmux.bak", ".codex/hooks.json.herdmux.bak"} {
			if _, ok := after[k]; !ok {
				t.Errorf("mcp=%v: backup %s missing", withMCP, k)
			}
		}
		if withMCP {
			if _, ok := after[".config/opencode/opencode.json.herdmux.bak"]; !ok {
				t.Error("opencode.json backup missing")
			}
		}
		for _, want := range []string{"keeps HERDMUX=1", "Kept ~/.config/herdmux"} {
			if !strings.Contains(out, want) {
				t.Errorf("uninstall output lacks %q:\n%s", want, out)
			}
		}
	}
}

func TestUninstallPurge(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.seed()
	h.mustInstall(nil)
	h.uninstall(false)
	if _, err := os.Stat(h.path(".config/herdmux/config.toml")); err != nil {
		t.Fatal("uninstall removed ~/.config/herdmux")
	}
	h.uninstall(true)
	if _, err := os.Stat(h.path(".config/herdmux")); err == nil {
		t.Fatal("--purge kept ~/.config/herdmux")
	}
	if h.read(".config/tmux/tmux.conf") != seedTmuxConf {
		t.Error("tmux.conf changed")
	}
}

func TestUninstallKeepsUserHooksInOurGroup(t *testing.T) {
	t.Parallel()
	h := newHome(t)
	h.mustInstall(nil)
	// The user added their own hook into one of herdmux's groups.
	s := strings.Replace(h.read(".claude/settings.json"), `"command": "herdmux hook claude"
          }
        ]
      }
    ],
    "Stop"`, `"command": "herdmux hook claude"
          },
          {
            "type": "command",
            "command": "mine"
          }
        ]
      }
    ],
    "Stop"`, 1)
	if !strings.Contains(s, `"mine"`) {
		t.Fatal("couldn't seed the mixed group")
	}
	h.write(".claude/settings.json", s)
	h.uninstall(false)
	got := h.read(".claude/settings.json")
	if strings.Contains(got, claude.Command) || !strings.Contains(got, `"command": "mine"`) {
		t.Errorf("settings.json after uninstall:\n%s", got)
	}
}

// The guard: nothing in this package may resolve, write or run anything
// under the real home inside a test binary.
func TestGuardRefusesTheRealHome(t *testing.T) {
	t.Parallel()
	if realHome == "" || within(filepath.Clean(os.TempDir()), realHome) {
		t.Skip("no real home outside the temp dir")
	}
	real := func(k string) string {
		if k == "HOME" {
			return realHome
		}
		return ""
	}
	if _, err := Resolve(real); err == nil {
		t.Fatal("Resolve accepted the real HOME")
	}
	for _, o := range []Options{{Getenv: real, Out: &bytes.Buffer{}}, {Getenv: real, Out: &bytes.Buffer{}, DryRun: true}} {
		if err := Install(o); err == nil || !strings.Contains(err.Error(), "may not touch") {
			t.Errorf("Install with the real HOME: %v", err)
		}
	}
	if err := Uninstall(Options{Getenv: real, Out: &bytes.Buffer{}, Purge: true}); err == nil {
		t.Error("Uninstall accepted the real HOME")
	}
	// The XDG override can't point there either.
	h := newHome(t)
	h.env["XDG_CONFIG_HOME"] = filepath.Join(realHome, ".config")
	if _, err := h.install(nil); err == nil {
		t.Error("install accepted XDG_CONFIG_HOME under the real home")
	}
	p := filepath.Join(realHome, ".herdmux-guard-test")
	if err := writeAtomic(p, []byte("x"), 0o644); err == nil {
		_ = os.Remove(p)
		t.Fatal("writeAtomic wrote under the real home")
	}
	if err := runAgentCLI(func(k string) string { return map[string]string{"PATH": "/usr/bin:/bin"}[k] }, Paths{Home: t.TempDir()}, []string{"true"}); err == nil {
		t.Error("runAgentCLI ran a binary outside the temp dir")
	}
}

func TestUnifiedDiff(t *testing.T) {
	t.Parallel()
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	want := `--- a
+++ b
@@ -1,6 +1,6 @@
 1
 2
-3
+three
 4
 5
 6
@@ -10,3 +10,4 @@
 10
 11
 12
+13
`
	if got := unifiedDiff("a", "b", []byte(a), []byte(b)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := unifiedDiff("/dev/null", "b", nil, []byte("x\n")); got != "--- /dev/null\n+++ b\n@@ -0,0 +1,1 @@\n+x\n" {
		t.Errorf("new file diff: %q", got)
	}
	if unifiedDiff("a", "b", []byte("x"), []byte("x")) != "" {
		t.Error("equal inputs gave a diff")
	}
}

func TestJSONRoundTripsCanonicalFiles(t *testing.T) {
	t.Parallel()
	for _, src := range []string{seedSettings, seedOpencode, "{}\n", "{\n\t\"a\": [\n\t\t1,\n\t\t\"<&>é\"\n\t]\n}"} {
		d, err := parseDoc([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(d.encode()); got != src {
			t.Errorf("round trip:\n%s\nwant:\n%s", got, src)
		}
	}
	for _, bad := range []string{"{", "[]", "{} {}", `{"a":}`} {
		if _, err := parseDoc([]byte(bad)); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}
