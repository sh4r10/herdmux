package statusbar

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// server starts a tmuxtest server with the colours and formats set (and a
// recheck loop slow enough to stay out of the way).
func server(t *testing.T) *tmuxtest.Server {
	t.Helper()
	s := tmuxtest.New(t)
	if err := colorscheme.Apply(s.Client, colorscheme.DefaultScheme()); err != nil {
		t.Fatal(err)
	}
	if err := initWith(s.Client, s.Home, time.Hour); err != nil {
		t.Fatal(err)
	}
	return s
}

var styleRE = regexp.MustCompile(`#\[[^]]*\]`)

func plain(s string) string { return styleRE.ReplaceAllString(s, "") }

// tab expands a window's tab format (current or not) as tmux draws it.
func tab(s *tmuxtest.Server, window string, current bool) string {
	o := "window-status-format"
	if current {
		o = "window-status-current-format"
	}
	return s.Display(window, "#{E:"+o+"}")
}

// agent marks pane as an agent in st with Attention att (as internal/state
// would have written it).
func agent(s *tmuxtest.Server, pane, st, att string) {
	s.Tmux("set-option", "-p", "-t", pane, state.OptAgent, "claude")
	s.Tmux("set-option", "-p", "-t", pane, state.OptState, st)
	if att != "" {
		s.Tmux("set-option", "-p", "-t", pane, state.OptAttention, att)
	}
}

func split(s *tmuxtest.Server, target string) string {
	return s.Tmux("split-window", "-d", "-t", target, "-P", "-F", "#{pane_id}")
}

const (
	bgNeeds = "#[bg=#d14d41]"
	bgDone  = "#[bg=#879a39]"
	bgTab   = "#[bg=#1c1b1a]"
	bgCur   = "#[bg=#403e3c]"
	onAtt   = "#[fg=#100f0f]"
)

func TestTabIcons(t *testing.T) {
	t.Parallel()
	s := server(t)
	w := s.Display(s.Pane, "#{window_id}")
	s.Tmux("rename-window", "-t", w, "work")
	p1, p2, p3 := split(s, w), split(s, w), split(s, w)
	agent(s, s.Pane, state.Working, "")
	// p1 is not an agent.
	agent(s, p2, state.Done, "")
	agent(s, p3, state.Unknown, "")
	// Expect one icon per agent pane, in tmux's pane order (each split
	// lands right after the active pane).
	icon := map[string]string{s.Pane: style.IconWorking + " ", p1: "", p2: style.IconDone + " ", p3: style.IconUnknown + " "}
	want := " "
	for _, p := range strings.Fields(s.Display(w, "#{P:#{pane_id} }")) {
		want += icon[p]
	}
	want += "0:work "
	if strings.Count(want, " ") != 5 {
		t.Fatalf("bad expectation %q", want)
	}
	got := tab(s, w, false)
	if plain(got) != want {
		t.Fatalf("tab text %q, want %q", plain(got), want)
	}
	// Coloured by state, nothing filled, no tmux flags.
	for _, sub := range []string{"#[fg=#d0a215]" + style.IconWorking, "#[fg=#879a39]" + style.IconDone, "#[fg=#878580]" + style.IconUnknown, bgTab} {
		if !strings.Contains(got, sub) {
			t.Errorf("tab %q lacks %q", got, sub)
		}
	}
	if strings.Contains(got, bgNeeds) || strings.Contains(got, bgDone) || strings.Contains(got, "*") {
		t.Errorf("tab without Attention: %q", got)
	}
}

func TestNoAgentsTab(t *testing.T) {
	t.Parallel()
	s := server(t)
	s.Tmux("rename-window", "-t", "main:0", "plain")
	if got := plain(tab(s, "main:0", false)); got != " 0:plain " {
		t.Fatalf("plain tab %q", got)
	}
}

func TestAttentionFill(t *testing.T) {
	t.Parallel()
	s := server(t)
	w := s.Display(s.Pane, "#{window_id}")
	p1 := split(s, w)
	agent(s, s.Pane, state.Done, "done")
	agent(s, p1, state.NeedsInput, "needs-input")

	// needs-input beats done in a mixed window; icons and name go dark.
	got := tab(s, w, false)
	if !strings.HasPrefix(got, bgNeeds+onAtt) || strings.Contains(got, bgDone) || strings.Contains(got, "#[fg=#d0a215]") {
		t.Fatalf("mixed: %q", got)
	}
	if !strings.Contains(plain(got), style.IconDone+" "+style.IconNeedsInput+" ") {
		t.Fatalf("mixed icons: %q", plain(got))
	}

	// Without herdmux running: break the needs-input pane out, and each
	// window shows its own fill.
	s.Tmux("break-pane", "-d", "-s", p1)
	w2 := s.Display(p1, "#{window_id}")
	if got := tab(s, w, false); !strings.HasPrefix(got, bgDone+onAtt) {
		t.Fatalf("after break-pane, old window: %q", got)
	}
	if got := tab(s, w2, false); !strings.HasPrefix(got, bgNeeds+onAtt) {
		t.Fatalf("after break-pane, new window: %q", got)
	}
	// join it back: needs-input wins again.
	s.Tmux("join-pane", "-d", "-s", p1, "-t", w)
	if got := tab(s, w, false); !strings.HasPrefix(got, bgNeeds) {
		t.Fatalf("after join-pane: %q", got)
	}
	// kill both agent panes' Attention sources: no fill.
	s.Tmux("kill-pane", "-t", p1)
	if got := tab(s, w, false); !strings.HasPrefix(got, bgDone) {
		t.Fatalf("after kill-pane: %q", got)
	}
	s.Tmux("set-option", "-p", "-u", "-t", s.Pane, state.OptAttention)
	if got := tab(s, w, false); !strings.HasPrefix(got, bgTab) {
		t.Fatalf("no Attention left: %q", got)
	}
}

func TestCurrentTabRaisedAndZoom(t *testing.T) {
	t.Parallel()
	s := server(t)
	w := s.Display(s.Pane, "#{window_id}")
	got := tab(s, w, true)
	if !strings.HasPrefix(got, bgCur+"#[fg=#cecdc3]#[bold]") || strings.Contains(got, "reverse") {
		t.Fatalf("current tab: %q", got)
	}
	if strings.Contains(got, style.IconZoom) {
		t.Fatalf("zoom icon unzoomed: %q", got)
	}
	split(s, w)
	s.Tmux("resize-pane", "-Z", "-t", s.Pane)
	if got := plain(tab(s, w, true)); !strings.HasSuffix(got, " "+style.IconZoom+" ") {
		t.Fatalf("zoomed tab: %q", got)
	}
	// An Attention tab that is current is filled and still bold.
	agent(s, s.Pane, state.Done, "done")
	if got := tab(s, w, true); !strings.HasPrefix(got, bgDone+onAtt+"#[bold]") {
		t.Fatalf("current Attention tab: %q", got)
	}
}

func TestStatusLeftAndNoShellJobs(t *testing.T) {
	t.Parallel()
	s := server(t)
	if got := s.Display(s.Pane, "#{E:status-left}"); got != "#[fg=#ce5d97]#[bold] main #[default] " {
		t.Fatalf("status-left %q", got)
	}
	for _, o := range []string{"status-left", "status-right", "window-status-format", "window-status-current-format"} {
		if v := s.Tmux("show-options", "-gv", o); strings.Contains(v, "#(") {
			t.Errorf("%s uses #(): %q", o, v)
		}
	}
	// No window-level options at all.
	if out := s.Tmux("show-options", "-w", "-t", "main:0"); strings.Contains(out, "@herdmux") {
		t.Errorf("window options: %q", out)
	}
}

func TestLiteral(t *testing.T) {
	t.Parallel()
	s := server(t)
	odd := filepath.Join(t.TempDir(), "a,b}#c")
	if err := os.Mkdir(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	s.Tmux("set-option", "-g", "status-right", StatusRight(odd))
	p := s.Tmux("new-window", "-d", "-c", odd, "-P", "-F", "#{pane_id}")
	if got := plain(s.Display(p, "#{E:status-right}")); got != "~ " {
		t.Fatalf("odd home: %q", got)
	}
}

// --- git ---

func repo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", branch)
	run("commit", "-q", "--allow-empty", "-m", "one")
	return dir
}

func right(s *tmuxtest.Server, pane string) string {
	return plain(s.Display(pane, "#{E:status-right}"))
}

func TestRefreshAndRecheck(t *testing.T) {
	t.Parallel()
	s := server(t)
	a, b := repo(t, "alpha"), repo(t, "beta")
	if err := os.WriteFile(filepath.Join(b, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pa := s.Tmux("new-window", "-d", "-c", a, "-P", "-F", "#{pane_id}")
	pb := split(s, pa)
	// Move pb into b: it's a split with a's cwd, so start it in b instead.
	s.Tmux("respawn-pane", "-k", "-t", pb, "-c", b)

	if err := Refresh(s.Client, pa); err != nil {
		t.Fatal(err)
	}
	if got := right(s, pa); got != filepath.Base(a)+" alpha " {
		t.Fatalf("refresh a: %q", got)
	}
	// pb hasn't been refreshed: only the directory shows.
	if got := right(s, pb); got != filepath.Base(b)+" " {
		t.Fatalf("unrefreshed b: %q", got)
	}
	if err := Refresh(s.Client, pb); err != nil {
		t.Fatal(err)
	}
	if got := right(s, pb); got != filepath.Base(b)+" beta "+DirtyMark+" " {
		t.Fatalf("refresh b: %q", got)
	}
	// The home directory shows as ~, with no git.
	ph := s.Tmux("new-window", "-d", "-c", s.Home, "-P", "-F", "#{pane_id}")
	if err := Recheck(s.Client); err != nil {
		t.Fatal(err)
	}
	if got := right(s, ph); got != "~ " {
		t.Fatalf("home: %q", got)
	}
	// Leaving the repo clears it (Recheck covers each session's active pane).
	s.Tmux("respawn-pane", "-k", "-t", pa, "-c", s.Home)
	s.Tmux("select-window", "-t", pa)
	s.Tmux("select-pane", "-t", pa)
	if err := Recheck(s.Client); err != nil {
		t.Fatal(err)
	}
	if got := s.PaneOption(pa, OptGitHead); got != "" {
		t.Fatalf("head after leaving the repo: %q", got)
	}
}

func TestStaleMarker(t *testing.T) {
	t.Parallel()
	s := server(t)
	s.Tmux("set-option", "-p", "-t", s.Pane, OptGitHead, "main")
	s.Tmux("set-option", "-p", "-t", s.Pane, OptGitStale, "1")
	if got := right(s, s.Pane); !strings.HasSuffix(got, " main"+StaleMark+" ") {
		t.Fatalf("stale: %q", got)
	}
}

func TestInitIdempotent(t *testing.T) {
	t.Parallel()
	s := server(t)
	if err := initWith(s.Client, s.Home, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, h := range GitHooks {
		out := s.Tmux("show-hooks", "-g", h)
		if n := strings.Count(out, "git-refresh"); n != 1 {
			t.Errorf("%s: %d herdmux hooks:\n%s", h, n, out)
		}
	}
}
