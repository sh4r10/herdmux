package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// End-to-end test of the sessionizer popup: the real popup on an attached
// client, one key per send-keys.

// A broken config.toml (a relative root) is shown in red in the popup, and
// the valid root is still listed: picking its directory creates the session.
func TestSessionizerConfigErrorSmoke(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	root := t.TempDir()
	dir := filepath.Join(root, "12.34-demo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, s.ConfigDir, map[string]string{
		"config.toml": "[sessionizer]\nroots = [\"projects\", \"" + root + "\"]\ndepth = 1\n",
	})
	c := s.Attach("main")
	herdmuxOK(t, s, "sessionizer", "--client", c.TTY)
	s.Eventually(5*time.Second, "the sessionizer with the config error", func() bool {
		return strings.Contains(c.Output(), `config.toml: [sessionizer] roots: "projects" is not an absolute`)
	})
	time.Sleep(150 * time.Millisecond) // bubbletea's first frame is up; keys now reach it
	for _, k := range []string{"d", "e", "m", "o", "Enter"} {
		key(s, c, k)
	}
	s.Eventually(5*time.Second, "the client on the new session", func() bool {
		return c.Format("#{session_name}") == "demo"
	})
	if got := c.Format("#{pane_current_path}"); got != dir {
		t.Errorf("new session's dir %q, want %q", got, dir)
	}
}
