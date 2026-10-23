package sessionizer

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

func TestSessionName(t *testing.T) {
	tests := []struct {
		dir  string
		want string
	}{
		{"12.34-herdmux", "herdmux"},
		{"herdmux", "herdmux"},
		{"123-project", "project"},
		{"---bare", "bare"},
		{"12.34.56-deeply.dashed-name", "deeply_dashed-name"},
		{"v1.2:beta", "v1_2_beta"},
		{".", "."},
		{"/", "/"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			if got := SessionName(tt.dir); got != tt.want {
				t.Errorf("SessionName(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

func TestWalkDirs(t *testing.T) {
	// Create a temp directory tree.
	root := t.TempDir()
	dirs := []string{
		"a",
		"a/b",
		"a/b/c",
		"a/b/c/d", // depth 3
		"x",
		"x/y",
		".hidden",
		".hidden/sub",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		depth int
		want  []string
	}{
		{0, []string{root}},
		{1, []string{
			root,
			filepath.Join(root, "a"),
			filepath.Join(root, "x"),
		}},
		{2, []string{
			root,
			filepath.Join(root, "a"),
			filepath.Join(root, "a", "b"),
			filepath.Join(root, "x"),
			filepath.Join(root, "x", "y"),
		}},
		{3, []string{
			root,
			filepath.Join(root, "a"),
			filepath.Join(root, "a", "b"),
			filepath.Join(root, "a", "b", "c"),
			filepath.Join(root, "x"),
			filepath.Join(root, "x", "y"),
		}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("depth=%d", tt.depth), func(t *testing.T) {
			got, err := walkDirs([]string{root}, tt.depth)
			if err != nil {
				t.Fatal(err)
			}
			// Convert to a set for comparison.
			gotSet := make(map[string]bool)
			for _, d := range got {
				gotSet[d] = true
			}
			wantSet := make(map[string]bool)
			for _, d := range tt.want {
				wantSet[d] = true
			}
			if len(gotSet) != len(wantSet) {
				t.Errorf("depth %d: got %d dirs, want %d", tt.depth, len(gotSet), len(wantSet))
			}
			for d := range wantSet {
				if !gotSet[d] {
					t.Errorf("depth %d: missing %q", tt.depth, d)
				}
			}
		})
	}
}

func TestHasSessionMatchesExactly(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("new-session", "-d", "-s", "foobar")

	for name, want := range map[string]bool{"foobar": true, "main": true, "foo": false, "ma": false, "nosuch": false} {
		got, err := HasSession(s.Client, name)
		if err != nil || got != want {
			t.Errorf("HasSession(%q) = %v, %v; want %v, nil", name, got, err, want)
		}
	}
}

func TestNewSession(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	dir := t.TempDir()

	pane, err := NewSession(s.Client, "proj", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Display(pane, "#{session_name} #{pane_current_path}"); got != "proj "+dir {
		t.Errorf("new pane %s: %q, want %q", pane, got, "proj "+dir)
	}
}

func TestSwitchTo(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach(tmuxtest.Session)
	on := func() string { return c.Format("#{client_session}") }
	count := func() int { return len(tmux.Lines(s.Tmux("list-sessions", "-F", "#{session_name}"))) }

	// A new project creates its session in the dir and switches to it.
	root := t.TempDir()
	dir := filepath.Join(root, "12.34-foobar")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwitchTo(s.Client, c.TTY, Entry{Type: "dir", Name: "12.34-foobar", Path: dir}); err != nil {
		t.Fatal(err)
	}
	if on() != "foobar" || count() != 2 {
		t.Fatalf("after new project: on %q with %d sessions, want foobar with 2", on(), count())
	}
	if got := s.Display("=foobar:", "#{pane_current_path}"); got != dir {
		t.Errorf("foobar's pane in %q, want %q", got, dir)
	}

	// "foo" is a prefix of the open "foobar": it gets its own session.
	foo := filepath.Join(root, "foo")
	if err := os.Mkdir(foo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwitchTo(s.Client, c.TTY, Entry{Type: "dir", Name: "foo", Path: foo}); err != nil {
		t.Fatal(err)
	}
	if on() != "foo" || count() != 3 {
		t.Fatalf("after prefix project: on %q with %d sessions, want foo with 3", on(), count())
	}

	// An existing project's dir just switches, without a new session.
	if err := SwitchTo(s.Client, c.TTY, Entry{Type: "dir", Name: "12.34-foobar", Path: dir}); err != nil {
		t.Fatal(err)
	}
	if on() != "foobar" || count() != 3 {
		t.Fatalf("after existing project: on %q with %d sessions, want foobar with 3", on(), count())
	}

	// A session entry switches by id.
	id := s.Display("=main:", "#{session_id};")
	if err := SwitchTo(s.Client, c.TTY, Entry{Type: "session", Name: "main", Session: id}); err != nil {
		t.Fatal(err)
	}
	if on() != "main" || count() != 3 {
		t.Fatalf("after session entry: on %q with %d sessions, want main with 3", on(), count())
	}
}

func TestListIncludesSessionsWithoutAgents(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	dir := t.TempDir()
	s.Tmux("new-session", "-d", "-s", "bare", "-c", dir)
	s.Tmux("set", "-p", "-t", s.Pane, state.OptAgent, "claude")
	s.Tmux("split-window", "-d", "-t", s.Pane)
	s.Tmux("set", "-p", "-t", "=main:.1", state.OptAgent, "pi")

	root := t.TempDir()
	got, err := List(s.Client, []string{root}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Type: "session", Name: "bare", Path: dir, Session: s.Display("=bare:", "#{session_id};"), Pane: s.Display("=bare:", "#{pane_id}")},
		{Type: "session", Name: "main", Path: s.Display("=main:", "#{session_path}"), Session: s.Display("=main:", "#{session_id};"), Agents: 2, Pane: s.Display("=main:", "#{pane_id}")},
		{Type: "dir", Name: filepath.Base(root), Path: root},
	}
	if !slices.Equal(got, want) {
		t.Errorf("List:\n got %+v\nwant %+v", got, want)
	}
}

// tmux turns "." and ":" in a new session's name into "_", so a dotted
// project dir must map to that name, or the second open makes a new session.
func TestSwitchToDottedDirTwice(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach(tmuxtest.Session)
	count := func() int { return len(tmux.Lines(s.Tmux("list-sessions", "-F", "#{session_name}"))) }

	for _, base := range []string{"12.34-herdmux", "12.35-my.proj"} {
		dir := filepath.Join(t.TempDir(), base)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		want := SessionName(dir)
		before := count()
		for i := range 2 {
			if err := SwitchTo(s.Client, c.TTY, Entry{Type: "dir", Name: base, Path: dir}); err != nil {
				t.Fatalf("%s open %d: %v", base, i+1, err)
			}
			if got := c.Format("#{client_session}"); got != want {
				t.Fatalf("%s open %d: on %q, want %q", base, i+1, got, want)
			}
			if n := count(); n != before+1 {
				t.Fatalf("%s open %d: %d sessions, want %d", base, i+1, n, before+1)
			}
		}
	}
}

// A project dir's name reaches new-session's -s and -c, which expand
// formats: a dir named "x#(cmd)" must not run cmd, and the session gets the
// literal name and path.
func TestSwitchToDirWithFormatInName(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	c := s.Attach(tmuxtest.Session)
	marker := filepath.Join(s.Home, "pwn")

	base := "x#(cd;touch pwn)#{session_id};"
	dir := filepath.Join(t.TempDir(), base)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwitchTo(s.Client, c.TTY, Entry{Type: "dir", Name: base, Path: dir}); err != nil {
		t.Fatal(err)
	}
	want := SessionName(dir)
	if got := c.Format("#{client_session}"); got != want {
		t.Errorf("on %q, want %q", got, want)
	}
	if got := s.Display("="+want+":", "#{pane_current_path}"); got != dir {
		t.Errorf("pane in %q, want %q", got, dir)
	}
	// #() jobs run in the background: give one time to land.
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("format in the dir name ran a shell command (%s exists)", marker)
	}
}
