package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdmux/internal/tmuxtest"
)

// End-to-end tests of config.toml, [keys] and launch.

func prefixKey(s *tmuxtest.Server, key string) string {
	out, _ := s.TmuxErr("list-keys", "-T", "prefix", key)
	return out
}

// fakeAgents is a config.toml whose agents only touch a marker file.
const fakeAgents = `
[[agent]]
name = "fake"
command = "touch launched-fake"

[[agent]]
name = "fake-two"
command = "touch launched-two"
`

func TestInitBindsKeys(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "init")
	for key, verb := range map[string]string{"a": "picker", "u": "next-attention", "r": "organiser", "o": "launch"} {
		if got := prefixKey(s, key); !strings.Contains(got, `run-shell -b "herdmux `+verb+` --client '#{client_name}'"`) {
			t.Errorf("prefix %s: %q", key, got)
		}
	}
	before := s.Tmux("list-keys")
	herdmuxOK(t, s, "init")
	if after := s.Tmux("list-keys"); after != before {
		t.Error("a second init changed the key table")
	}

	// A hand edit applies on the next init: prefix r goes back to tmux's
	// refresh-client, and the colorscheme picker gets a root key.
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": "[keys]\norganiser = \"prefix R\"\ncolorscheme = \"M-c\"\n"})
	herdmuxOK(t, s, "init")
	if got := prefixKey(s, "r"); got != "bind-key -T prefix r refresh-client" {
		t.Errorf("prefix r: %q", got)
	}
	if got := prefixKey(s, "R"); !strings.Contains(got, "herdmux organiser") {
		t.Errorf("prefix R: %q", got)
	}
	if got, _ := s.TmuxErr("list-keys", "-T", "root", "M-c"); !strings.Contains(got, "herdmux colorscheme") {
		t.Errorf("root M-c: %q", got)
	}
}

func TestInitBrokenConfigUsesDefaultKeys(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": "[keys]\norganiser = \"prefix R\"\n[[agent\n"})
	herdmuxOK(t, s, "init")
	if got := prefixKey(s, "r"); !strings.Contains(got, "herdmux organiser") {
		t.Errorf("prefix r: %q (want the default organiser key)", got)
	}
	if got := prefixKey(s, "R"); got != "" {
		t.Errorf("prefix R: %q", got)
	}
	if log := readFile(t, s.LogPath()); !strings.Contains(log, "init: config.toml: line ") {
		t.Errorf("log %q", log)
	}
}

func TestInitBrokenConfigShowsRedMessage(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach("main")
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": "[keys]\npicker = \"prefix o\"\n"})
	herdmuxOK(t, s, "init")
	s.Eventually(3*time.Second, "the red config.toml message", func() bool {
		return strings.Contains(c.Output(), `herdmux: config.toml: [keys] picker and launch are both "prefix o"`)
	})
}

// windowIn creates a window whose shell sits in a new dir, and returns its
// pane and the dir.
func windowIn(t *testing.T, s *tmuxtest.Server, session string) (pane, dir string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pane = s.Tmux("new-window", "-t", session+":", "-c", dir, "-P", "-F", "#{pane_id}")
	return pane, dir
}

func TestLaunchByName(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": fakeAgents})
	from, dir := windowIn(t, s, "main")
	r := s.Run(tmuxtest.Cmd{Args: []string{"launch", "fake-two"}, Pane: from})
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.Code, r.Stderr)
	}
	var pane string
	for _, l := range s.ListPanes(tmuxShowFields) {
		if f := strings.Split(l, "\t"); f[1] == "fake-two" {
			pane = f[0]
			if f[2] != dir {
				t.Errorf("new window's dir %q, want %q", f[2], dir)
			}
			if f[3] != "1" {
				t.Error("the new window is not the session's current window")
			}
		}
	}
	if pane == "" {
		t.Fatalf("no window named fake-two:\n%s", strings.Join(s.ListPanes(tmuxShowFields), "\n"))
	}
	marker := filepath.Join(dir, "launched-two")
	s.Eventually(5*time.Second, "the typed command to run", func() bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	// The command has exited; its shell is still there.
	time.Sleep(200 * time.Millisecond)
	if got := s.Display(pane, "#{pane_id} #{pane_dead} #{pane_current_command}"); got != pane+" 0 sh" {
		t.Errorf("after the command exited: %q", got)
	}
}

const tmuxShowFields = "#{pane_id}\t#{window_name}\t#{pane_current_path}\t#{window_active}"

func TestLaunchSwitchesClient(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": fakeAgents})
	s.Tmux("new-session", "-d", "-s", "other")
	_, dir := windowIn(t, s, "other")
	c := s.Attach("other")
	// As from a key binding, run from a pane in another session (main): the
	// launch goes to the client's session and its current pane's dir.
	r := s.Run(tmuxtest.Cmd{Args: []string{"launch", "fake", "--client", c.TTY}})
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.Code, r.Stderr)
	}
	got := c.Format("#{session_name} #{window_name} #{pane_current_path}")
	if got != "other fake "+dir {
		t.Errorf("client shows %q", got)
	}
}

func TestLaunchFromPaneFollowsClient(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": fakeAgents})
	c := s.Attach("main")
	herdmuxOK(t, s, "launch", "fake")
	if got := c.Format("#{window_name}"); got != "fake" {
		t.Errorf("client shows window %q", got)
	}
}

func TestLaunchUnknownAgent(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	r := s.Herdmux("launch", "nope")
	if r.Code != 1 || !strings.Contains(r.Stderr, `no agent "nope" in config.toml`) {
		t.Errorf("code=%d stderr=%q", r.Code, r.Stderr)
	}
	// From a key binding (--client) the error goes to the client in red.
	c := s.Attach("main")
	r = s.Herdmux("launch", "nope", "--client", c.TTY)
	if r.Code != 1 || r.Stderr != "" {
		t.Errorf("--client: code=%d stderr=%q", r.Code, r.Stderr)
	}
	s.Eventually(3*time.Second, "the red message", func() bool {
		return strings.Contains(c.Output(), `herdmux launch: no agent "nope"`)
	})
}

// The smoke run: prefix o on a real client opens the prompt popup, one key
// per send-keys launches.
func TestLaunchPromptSmoke(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"config.toml": fakeAgents})
	_, dir := windowIn(t, s, "main")
	c := s.Attach("main")
	herdmuxOK(t, s, "init")
	s.Tmux("send-keys", "-K", "-c", c.TTY, "C-b")
	s.Tmux("send-keys", "-K", "-c", c.TTY, "o")
	s.Eventually(5*time.Second, "the launch prompt", func() bool {
		return strings.Contains(c.Output(), "q close")
	})
	s.Tmux("send-keys", "-K", "-c", c.TTY, "a") // fake-two's auto-picked key
	s.Eventually(5*time.Second, "the launched window", func() bool {
		return c.Format("#{window_name}") == "fake-two"
	})
	if got := c.Format("#{pane_current_path}"); got != dir {
		t.Errorf("launched in %q, want %q", got, dir)
	}
	s.Eventually(5*time.Second, "the typed command to run", func() bool {
		_, err := os.Stat(filepath.Join(dir, "launched-two"))
		return err == nil
	})
}
