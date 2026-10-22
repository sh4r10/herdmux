package launch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"herdmux/internal/config"
	"herdmux/internal/tmuxtest"
)

// new-window's -n and -c expand formats: an agent name or a pane directory
// with "#(cmd)" in it must not run cmd, and the window gets them literally.
func TestLaunchDoesNotExpandFormats(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	marker := filepath.Join(s.Home, "pwn")
	dir := filepath.Join(t.TempDir(), "d#(cd;touch pwn)#{session_id};")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "a#(cd;touch pwn)#{pane_id};"
	session := s.Display(tmuxtest.Session+":", "#{session_id};")

	pane, err := Launch(s.Client, config.Agent{Name: name, Command: "true"}, Target{Session: session, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Display(pane, "#{window_name}"); got != name {
		t.Errorf("window name %q, want %q", got, name)
	}
	if got := s.Display(pane, "#{pane_current_path}"); got != dir {
		t.Errorf("pane in %q, want %q", got, dir)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("format in the name or dir ran a shell command (%s exists)", marker)
	}
}
