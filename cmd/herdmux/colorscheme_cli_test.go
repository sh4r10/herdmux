package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// End-to-end tests of colorschemes: init's apply and
// fallback, and the colorscheme verb.

// schemeFile is a valid scheme body (gruvbox-dark's colours) with edits:
// a "" value drops the slot.
func schemeFile(edits map[string]string) string {
	g, _ := colorscheme.BundledScheme("gruvbox-dark")
	var b strings.Builder
	b.WriteString("[colors]\n")
	for _, slot := range colorscheme.Slots {
		v := g.Colors[slot]
		if e, ok := edits[slot]; ok {
			v = e
		}
		if v != "" {
			b.WriteString(slot + ` = "` + v + "\"\n")
		}
	}
	return b.String()
}

func writeConfig(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestInitAppliesSavedScheme(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{"colorscheme": "gruvbox-dark\n"})
	herdmuxOK(t, s, "init")
	if got := s.Tmux("show-options", "-gv", colorscheme.OptActive); got != "gruvbox-dark" {
		t.Fatalf("active %q", got)
	}
	if got := s.Tmux("show-options", "-gv", "@herdmux_c_accent"); got != "#fabd2f" {
		t.Errorf("accent %q", got)
	}
	// A tmux.conf reload (another theme's styles, then init again) restores it.
	s.Tmux("set-option", "-g", "status-style", "bg=red")
	s.Tmux("set-option", "-gw", "window-active-style", "bg=blue")
	herdmuxOK(t, s, "init")
	if got := s.Tmux("show-options", "-gv", "status-style"); got != "bg=#1d2021,fg=#ebdbb2" {
		t.Errorf("status-style after reload %q", got)
	}
	if got := s.Tmux("show-options", "-gv", "window-active-style"); got != "bg=#282828,fg=#ebdbb2" {
		t.Errorf("window-active-style after reload %q", got)
	}
}

func TestInitDefaultsToFlexokiDark(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "init")
	if got := s.Tmux("show-options", "-gv", colorscheme.OptActive); got != colorscheme.Default {
		t.Fatalf("active %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "colorscheme")); err == nil {
		t.Error("init wrote the saved choice")
	}
}

func TestInitFallsBackWithMessage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string // in the tmux message and the log
	}{
		{"unknown", map[string]string{"colorscheme": "nope\n"},
			[]string{`colorscheme "nope"`, "not found; using flexoki-dark"}},
		{"invalid", map[string]string{"colorscheme": "neon\n",
			"colors/neon.toml": schemeFile(map[string]string{"border": "#12345", "attention_done": ""})},
			[]string{"colors/neon.toml", `border = "#12345" is not #rrggbb`, "attention_done missing", "using flexoki-dark"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tmuxtest.New(t)
			writeConfig(t, s.ConfigDir, tc.files)
			c := s.Attach("main")
			herdmuxOK(t, s, "init")
			if got := s.Tmux("show-options", "-gv", colorscheme.OptActive); got != colorscheme.Default {
				t.Errorf("active %q", got)
			}
			if got := readFile(t, filepath.Join(s.ConfigDir, "colorscheme")); got != tc.files["colorscheme"] {
				t.Errorf("saved choice changed to %q", got)
			}
			// The message shows on the attached client (the end of a long
			// path may be cut, so look for the last part).
			last := tc.want[len(tc.want)-1]
			s.Eventually(3*time.Second, "the fallback message", func() bool { return strings.Contains(c.Output(), last) })
			log := readFile(t, s.LogPath())
			for _, w := range tc.want {
				if !strings.Contains(log, w) {
					t.Errorf("log lacks %q:\n%s", w, log)
				}
			}
		})
	}
}

func TestColorschemeSetCLI(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	writeConfig(t, s.ConfigDir, map[string]string{
		"colors/neon.toml": schemeFile(map[string]string{"fg": ""}),
	})
	saved := filepath.Join(s.ConfigDir, "colorscheme")

	r := herdmuxOK(t, s, "colorscheme", "gruvbox-dark")
	if !strings.Contains(r.Stdout, "gruvbox-dark") {
		t.Errorf("stdout %q", r.Stdout)
	}
	if got := readFile(t, saved); got != "gruvbox-dark\n" {
		t.Errorf("saved %q", got)
	}
	if got := s.Tmux("show-options", "-gv", "pane-active-border-style"); got != "fg=#83a598" {
		t.Errorf("not applied: %q", got)
	}

	for _, name := range []string{"nope", "neon"} {
		r := s.Herdmux("colorscheme", name)
		if r.Code != 1 || r.Stderr == "" {
			t.Errorf("colorscheme %s: code=%d stderr=%q", name, r.Code, r.Stderr)
		}
		if got := readFile(t, saved); got != "gruvbox-dark\n" {
			t.Errorf("colorscheme %s changed the saved choice to %q", name, got)
		}
	}
	if got := s.Tmux("show-options", "-gv", colorscheme.OptActive); got != "gruvbox-dark" {
		t.Errorf("a refused scheme was applied: %q", got)
	}
	if r := s.Herdmux("colorscheme"); r.Code != 1 || !strings.Contains(r.Stderr, "no client") {
		t.Errorf("picker with no client: code=%d stderr=%q", r.Code, r.Stderr)
	}
}

func TestColorschemeListCLI(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, "config")
	writeConfig(t, dir, map[string]string{
		"colorscheme":                  "midnight-moss\n",
		"colors/tokyonight-night.toml": schemeFile(nil),
		"colors/midnight-moss.toml":    schemeFile(nil),
		"colors/broken-neon.toml":      schemeFile(map[string]string{"border": "#12345", "attention_done": ""}),
	})
	// Outside tmux: --list works, the other forms need tmux.
	r := runCLI(t, map[string]string{"HERDMUX_CONFIG_DIR": dir}, "colorscheme", "--list")
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	rows := map[string][]string{}
	table, _, _ := strings.Cut(r.stdout, "\n\n")
	for _, l := range strings.Split(table, "\n") {
		f := strings.Fields(strings.TrimPrefix(l, "*"))
		if len(f) >= 4 {
			rows[f[0]] = append([]string{l}, f[1:]...)
		}
	}
	if len(rows) != 28 { // header + 25 + 2 user
		t.Errorf("%d rows:\n%s", len(rows), r.stdout)
	}
	check := func(name, want string) {
		if !strings.Contains(rows[name][0], want) {
			t.Errorf("%s row lacks %q: %q", name, want, rows[name][0])
		}
	}
	check("tokyonight-night", "override")
	check("midnight-moss", "user")
	check("broken-neon", "invalid: bad border, no attention_done")
	check("flexoki-dark", "bundled")
	check("catppuccin-latte", "light")
	if !strings.HasPrefix(rows["midnight-moss"][0], "*") {
		t.Errorf("saved scheme not marked: %q", rows["midnight-moss"][0])
	}
	if strings.Contains(r.stdout, "active") {
		t.Errorf("outside tmux nothing is active:\n%s", r.stdout)
	}

	for _, args := range [][]string{{"colorscheme"}, {"colorscheme", "gruvbox-dark"}} {
		if r := runCLI(t, map[string]string{"HERDMUX_CONFIG_DIR": dir}, args...); r.code != 2 || r.stderr != "herdmux: not inside tmux\n" {
			t.Errorf("%v outside tmux: code=%d stderr=%q", args, r.code, r.stderr)
		}
	}
	if got := readFile(t, filepath.Join(dir, "colorscheme")); got != "midnight-moss\n" {
		t.Errorf("saved changed to %q", got)
	}
}

func TestColorschemeListMarksActive(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "colorscheme", "kanagawa-wave")
	r := herdmuxOK(t, s, "colorscheme", "--list")
	table, _, _ := strings.Cut(r.Stdout, "\n\n")
	n := 0
	for _, l := range strings.Split(table, "\n") {
		if strings.Contains(l, "active") {
			n++
			if !strings.HasPrefix(l, "*") || !strings.Contains(l, " kanagawa-wave ") {
				t.Errorf("active row %q", l)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d active rows:\n%s", n, r.Stdout)
	}
}

// Every herdmux run inside tmux reconciles first,
// including the read-only `colorscheme --list`.
func TestColorschemeListReconciles(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "report", "working", "--agent", "claude", "--pid", "99999999")
	if got := s.PaneOption(s.Pane, state.OptAgent); got != "claude" {
		t.Fatalf("after report: agent %q", got)
	}
	herdmuxOK(t, s, "colorscheme", "--list")
	if got := s.PaneOption(s.Pane, state.OptAgent); got != "" {
		t.Errorf("colorscheme --list kept the dead agent: %q", got)
	}
}
