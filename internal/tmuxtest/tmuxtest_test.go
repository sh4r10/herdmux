package tmuxtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

func TestServerStartsOnTestSocket(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if !strings.HasPrefix(s.Name, "herdmux-test-") {
		t.Fatalf("socket name %q", s.Name)
	}
	if filepath.Base(s.SocketPath) != s.Name {
		t.Fatalf("socket path %q does not match name %q", s.SocketPath, s.Name)
	}
	if got := s.Display("", "#{socket_path}"); got != s.SocketPath {
		t.Fatalf("display socket_path = %q", got)
	}
	if got := s.ListPanes("#{session_name} #{pane_id}"); len(got) != 1 || got[0] != "main "+s.Pane {
		t.Fatalf("panes = %q", got)
	}
}

func TestPaneEnvironmentIsScrubbed(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	dump := filepath.Join(s.Home, "env.txt")
	s.Tmux("new-window", "-d", "env > "+dump+"; touch "+dump+".done")
	s.Eventually(5*time.Second, "the env dump", func() bool {
		_, err := os.Stat(dump + ".done")
		return err == nil
	})
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch {
		case strings.HasPrefix(k, "HERDR_"):
			t.Errorf("pane has %s", line)
		case k == "TMUX" && !strings.HasPrefix(v, s.SocketPath+","):
			t.Errorf("pane TMUX %q is not the test server", v)
		case k == "HOME" && v != s.Home:
			t.Errorf("pane HOME %q, want %q", v, s.Home)
		case k == "HERDMUX_CONFIG_DIR" && v != s.ConfigDir:
			t.Errorf("pane HERDMUX_CONFIG_DIR %q", v)
		}
	}
	// The server's own environment must not carry an inherited TMUX either.
	if out, _ := s.TmuxErr("show-environment", "-g", "TMUX"); strings.HasPrefix(out, "TMUX=") {
		t.Errorf("server global environment has %q", out)
	}
}

func TestServerGoneAfterCleanup(t *testing.T) {
	t.Parallel()
	var name, sock string
	t.Run("inner", func(t *testing.T) {
		s := tmuxtest.New(t)
		name, sock = s.Name, s.SocketPath
	})
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket %s still exists: %v", sock, err)
	}
	c, err := tmux.OnSocket(name)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := c.Run("list-sessions"); err == nil {
		t.Errorf("server %s still answers: %q", name, out)
	}
}

func TestParallelServersGetDifferentSockets(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := map[string]bool{}
	for i := range 4 {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := tmuxtest.New(t)
			s.Tmux("set", "-g", "@which", string(rune('a'+i)))
			mu.Lock()
			defer mu.Unlock()
			if seen[s.Name] {
				t.Errorf("socket %s reused", s.Name)
			}
			seen[s.Name] = true
		})
	}
}

func TestHerdmuxRunsAgainstServer(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if r := s.Herdmux("--version"); r.Code != 0 || r.Stdout != "herdmux 1.4.0\n" {
		t.Fatalf("--version: %+v", r)
	}
	if r := s.Run(tmuxtest.Cmd{Args: []string{"list"}, Outside: true}); r.Code != 2 {
		t.Fatalf("list outside tmux: %+v", r)
	}
	// Inside the server, a never-fail verb exits 0 and logs its error into the temp HOME.
	if r := s.Herdmux("tmux-hook", "pane-focus-in"); r.Code != 0 {
		t.Fatalf("tmux-hook: %+v", r)
	}
	if b, err := os.ReadFile(s.LogPath()); err != nil || !strings.Contains(string(b), "tmux-hook") {
		t.Fatalf("log: %v %q", err, b)
	}
	// herdmux is on the server's PATH, for hooks and bindings that call it bare.
	s.Tmux("run-shell", "herdmux --version > "+filepath.Join(s.Home, "v.txt"))
	if b, _ := os.ReadFile(filepath.Join(s.Home, "v.txt")); string(b) != "herdmux 1.4.0\n" {
		t.Fatalf("herdmux from run-shell: %q", b)
	}
}

func TestAttachedClientFocus(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not installed")
	}
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "focus-events", "on")
	c := s.Attach(tmuxtest.Session)

	clients := tmux.Lines(s.Tmux("list-clients", "-F", "#{client_tty} #{client_session}"))
	if len(clients) != 1 || clients[0] != c.TTY+" main" {
		t.Fatalf("list-clients = %q, want %q", clients, c.TTY+" main")
	}
	if got := s.Display(s.Pane, "#{window_active_clients}"); got != "1" {
		t.Fatalf("window_active_clients = %q", got)
	}
	focused := func() bool { return strings.Contains(c.Format("#{client_flags}"), "focused") }

	c.FocusIn()
	s.Eventually(3*time.Second, "client focused", focused)
	c.FocusOut()
	s.Eventually(3*time.Second, "client unfocused", func() bool { return !focused() })
	c.FocusIn()
	s.Eventually(3*time.Second, "client focused again", focused)

	c.Close()
	s.Eventually(3*time.Second, "client gone", func() bool {
		out, _ := s.TmuxErr("list-clients")
		return out == ""
	})
}

// An XDG_CONFIG_HOME (or HERDMUX_CONFIG_DIR) inherited from the developer's
// shell must never reach a test server: herdmux would resolve its config dir
// to the real one. parent stands in for the real config home; nothing may
// be written under it, and the server and its herdmux runs see the test's
// own dirs. Not parallel: it sets the process environment.
func TestInheritedConfigEnvIsReplaced(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", parent)
	t.Setenv("HERDMUX_CONFIG_DIR", filepath.Join(parent, "herdmux"))
	s := tmuxtest.New(t)

	if within(s.ConfigDir, parent) {
		t.Fatalf("ConfigDir %q is under the inherited %q", s.ConfigDir, parent)
	}
	for _, env := range [][]string{s.OutsideEnv(), s.Env(s.Pane)} {
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			if (k == "XDG_CONFIG_HOME" || k == "HERDMUX_CONFIG_DIR") && within(v, parent) {
				t.Errorf("herdmux env has %s", kv)
			}
		}
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "HERDMUX_CONFIG_DIR"} {
		if out, _ := s.TmuxErr("show-environment", "-g", k); strings.Contains(out, parent) {
			t.Errorf("server global environment has %q", out)
		}
	}

	// herdmux writes its config dir (the saved colorscheme): it lands in the
	// test's dir, never under parent.
	if r := s.Herdmux("colorscheme", "gruvbox-dark"); r.Code != 0 {
		t.Fatalf("colorscheme: code=%d stderr=%q", r.Code, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "colorscheme")); err != nil {
		t.Errorf("the saved colorscheme is not in the test config dir: %v", err)
	}
	// (The go tool, building the binary, may keep its own go/ there.)
	if ents, _ := os.ReadDir(parent); slices.ContainsFunc(ents, func(e os.DirEntry) bool { return e.Name() != "go" }) {
		t.Errorf("herdmux wrote under the inherited config home: %v", ents)
	}
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
