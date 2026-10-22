package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdmux/internal/config"
	"herdmux/internal/integrations"
)

// Paths are every file install and uninstall read or write. They all
// resolve from the passed-in $HOME and $XDG_CONFIG_HOME, never the process
// environment, so a test's temp HOME is the only home it can reach.
type Paths struct {
	Home           string
	TmuxConf       string // $XDG_CONFIG_HOME/tmux/tmux.conf, default ~/.config/tmux/tmux.conf
	ClaudeSettings string // ~/.claude/settings.json (hooks; permissions with --mcp)
	ClaudeJSON     string // ~/.claude.json: read only, to see whether `claude mcp add` already ran
	OpencodePlugin string // $XDG_CONFIG_HOME/opencode/plugins/herdmux-agent-state.js
	OpencodeJSON   string // $XDG_CONFIG_HOME/opencode/opencode.json (--mcp)
	PiExtension    string // ~/.pi/agent/extensions/herdmux-agent-state.ts
	PiMCP          string // ~/.pi/agent/mcp.json: read only, written by `pi mcp add`
	ConfigDir      string // herdmux's own config dir, config.Dir (only uninstall --purge touches it)
}

// Resolve returns the paths for the environment getenv describes.
func Resolve(getenv func(string) string) (Paths, error) {
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return Paths{}, errors.New("HOME is not set to an absolute path")
	}
	home = filepath.Clean(home)
	xdg, err := config.XDGConfigHome(getenv)
	if err != nil {
		return Paths{}, err
	}
	cfg, err := config.Dir(getenv)
	if err != nil {
		return Paths{}, err
	}
	p := Paths{
		Home:           home,
		TmuxConf:       filepath.Join(xdg, "tmux", "tmux.conf"),
		ClaudeSettings: filepath.Join(home, ".claude", "settings.json"),
		ClaudeJSON:     filepath.Join(home, ".claude.json"),
		OpencodePlugin: filepath.Join(xdg, "opencode", "plugins", integrations.OpencodePluginName),
		OpencodeJSON:   filepath.Join(xdg, "opencode", "opencode.json"),
		PiExtension:    filepath.Join(home, ".pi", "agent", "extensions", integrations.PiExtensionName),
		PiMCP:          filepath.Join(home, ".pi", "agent", "mcp.json"),
		ConfigDir:      cfg,
	}
	for _, f := range []string{p.TmuxConf, p.ClaudeSettings, p.ClaudeJSON, p.OpencodePlugin, p.OpencodeJSON, p.PiExtension, p.PiMCP, p.ConfigDir} {
		if err := guard(f); err != nil {
			return Paths{}, err
		}
	}
	return p, nil
}

// realHome is the test process's own home, captured before any test runs.
var realHome = func() string {
	h := os.Getenv("HOME")
	if h == "" {
		h, _ = os.UserHomeDir()
	}
	return filepath.Clean(h)
}()

// guard fails closed inside a test binary: any path under the real $HOME
// (and not under the temp dir) is refused. Every write, removal and exec
// goes through it as well as Resolve, so a test that forgets its temp HOME
// errors instead of editing the user's tmux.conf or agent config.
func guard(path string) error {
	if !testing.Testing() {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if within(abs, filepath.Clean(os.TempDir())) {
		return nil
	}
	if realHome == "" || realHome == "/" || within(abs, realHome) {
		return fmt.Errorf("install: test binaries may not touch %s (under the real home %s)", abs, realHome)
	}
	return nil
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// tilde shortens a path under home for messages.
func (p Paths) tilde(path string) string {
	if within(path, p.Home) {
		return "~" + strings.TrimPrefix(path, p.Home)
	}
	return path
}
