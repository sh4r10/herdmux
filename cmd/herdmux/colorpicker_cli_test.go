package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// End-to-end tests of the colorscheme picker popup: the
// real popup on an attached client, one key per send-keys.

func showColorpicker(t *testing.T, s *tmuxtest.Server, c *tmuxtest.Client) {
	t.Helper()
	herdmuxOK(t, s, "colorscheme", "--client", c.TTY)
	s.Eventually(5*time.Second, "the colorscheme picker", func() bool {
		return strings.Contains(c.Output(), "first/last")
	})
	time.Sleep(150 * time.Millisecond) // bubbletea's first frame is up; keys now reach it
}

func activeScheme(s *tmuxtest.Server) string {
	return s.Tmux("show-options", "-gqv", colorscheme.OptActive)
}

// The smoke run: j restyles the server live, ⏎ saves it.
func TestColorpickerSaveSmoke(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach("main")
	herdmuxOK(t, s, "init") // flexoki-dark, nothing saved
	showColorpicker(t, s, c)

	key(s, c, "j")
	s.Eventually(3*time.Second, "the live restyle", func() bool { return activeScheme(s) == "flexoki-light" })
	key(s, c, "Enter")
	saved := filepath.Join(s.ConfigDir, "colorscheme")
	s.Eventually(3*time.Second, "the saved choice", func() bool { return readFile(t, saved) == "flexoki-light\n" })
	light, _ := colorscheme.BundledScheme("flexoki-light")
	if got := s.Tmux("show-options", "-gv", "status-style"); got != "bg="+light.Colors["bg"]+",fg="+light.Colors["fg"] {
		t.Errorf("status-style %q", got)
	}
	if got := activeScheme(s); got != "flexoki-light" {
		t.Errorf("active %q", got)
	}
}

// Esc reverts the live preview to the saved scheme, and a save made
// elsewhere while the picker is open (here `herdmux colorscheme <name>` from
// a pane, as MCP set_colorscheme does) becomes what Esc reverts to.
func TestColorpickerRevertAndSavedElsewhere(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach("main")
	writeConfig(t, s.ConfigDir, map[string]string{"colorscheme": "gruvbox-dark\n"})
	herdmuxOK(t, s, "init")
	showColorpicker(t, s, c)

	key(s, c, "j")
	s.Eventually(3*time.Second, "the live restyle", func() bool { return activeScheme(s) == "gruvbox-light" })
	key(s, c, "Escape")
	s.Eventually(3*time.Second, "the revert", func() bool { return activeScheme(s) == "gruvbox-dark" })

	showColorpicker(t, s, c)
	key(s, c, "k")
	s.Eventually(3*time.Second, "the live restyle", func() bool { return activeScheme(s) == "rose-pine-dawn" })
	herdmuxOK(t, s, "colorscheme", "kanagawa-wave")
	s.Eventually(3*time.Second, "the saved-elsewhere footer", func() bool {
		return strings.Contains(c.Output(), "saved elsewhere (MCP): kanagawa-wave")
	})
	key(s, c, "j") // a preview after the outside save
	s.Eventually(3*time.Second, "the live restyle", func() bool { return activeScheme(s) == "kanagawa-dragon" })
	key(s, c, "Escape")
	s.Eventually(3*time.Second, "the revert to the outside save", func() bool { return activeScheme(s) == "kanagawa-wave" })
	if got := readFile(t, filepath.Join(s.ConfigDir, "colorscheme")); got != "kanagawa-wave\n" {
		t.Errorf("saved %q", got)
	}
}
