package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"herdmux/internal/statusbar"
	"herdmux/internal/tmuxtest"
)

// End-to-end tests of the status bar: herdmux init, the git
// hooks and the recheck loop, through the built binary.

func gitRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", branch}, {"commit", "-q", "--allow-empty", "-m", "one"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

var styleRE = regexp.MustCompile(`#\[[^]]*\]`)

// rightSide is status-right as the client showing pane would draw it, minus
// styles.
func rightSide(s *tmuxtest.Server, pane string) string {
	return styleRE.ReplaceAllString(s.Display(pane, "#{E:status-right}"), "")
}

// recheckLoops counts live processes running the recheck loop of generation gen.
func recheckLoops(t *testing.T, gen string) int {
	t.Helper()
	dirs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	n := 0
	for _, d := range dirs {
		b, err := os.ReadFile(d)
		if err == nil && strings.Contains(string(b), "g="+gen+";") {
			n++
		}
	}
	return n
}

func TestInitStatusbarIdempotent(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	herdmuxOK(t, s, "init")
	gen1 := s.Tmux("show-options", "-gv", statusbar.OptRecheck)
	s.Eventually(2*time.Second, "first recheck loop", func() bool { return recheckLoops(t, gen1) == 1 })

	herdmuxOK(t, s, "init")
	gen2 := s.Tmux("show-options", "-gv", statusbar.OptRecheck)
	if gen2 == gen1 {
		t.Fatal("init kept the recheck generation")
	}
	for _, h := range append(statusbar.GitHooks, "pane-focus-in") {
		out := s.Tmux("show-hooks", "-g", h)
		if n := strings.Count(out, "herdmux tmux-hook"); n != 1 {
			t.Errorf("%s: %d herdmux hooks:\n%s", h, n, out)
		}
	}
	// The old loop exits at its next tick; exactly one loop is left.
	s.Eventually(statusbar.RecheckInterval+3*time.Second, "old recheck loop to exit", func() bool {
		return recheckLoops(t, gen1) == 0 && recheckLoops(t, gen2) == 1
	})
}

func TestGitHooksAndRecheck(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	a, b := gitRepo(t, "alpha"), gitRepo(t, "beta")
	if err := os.WriteFile(filepath.Join(b, "untracked"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	herdmuxOK(t, s, "init")
	if got := rightSide(s, s.Pane); got != "~ " {
		t.Fatalf("home: %q", got)
	}

	// Selecting a window whose active pane is in another repo refreshes it
	// (session-window-changed).
	pb := s.Tmux("new-window", "-c", b, "-P", "-F", "#{pane_id}")
	want := filepath.Base(b) + " beta " + statusbar.DirtyMark + " "
	s.Eventually(3*time.Second, "beta on the right", func() bool { return rightSide(s, pb) == want })

	// Splitting into a (window-pane-changed) refreshes the new active pane.
	pa := s.Tmux("split-window", "-c", a, "-t", pb, "-P", "-F", "#{pane_id}")
	s.Eventually(3*time.Second, "alpha on the right", func() bool { return rightSide(s, pa) == filepath.Base(a)+" alpha " })

	// A cd fires no hook; the recheck loop catches it within one interval.
	s.Tmux("send-keys", "-t", pa, "cd "+b, "Enter")
	s.Eventually(statusbar.RecheckInterval+3*time.Second, "recheck after cd", func() bool { return rightSide(s, pa) == want })
}

func TestTmuxHookGitErrorsExit0(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	for _, args := range [][]string{
		{"tmux-hook", "git-refresh"},
		{"tmux-hook", "git-refresh", "%999"},
		{"tmux-hook", "recheck", "extra"},
	} {
		if r := s.Herdmux(args...); r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, r.Code, r.Stdout, r.Stderr)
		}
	}
	herdmuxOK(t, s, "tmux-hook", "recheck")
	log, _ := os.ReadFile(s.LogPath())
	for _, want := range []string{"git-refresh wants the pane id", "%999", "recheck takes no arguments"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
