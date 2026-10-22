package tmux_test

import (
	"errors"
	"strings"
	"testing"

	"herdmux/internal/tmux"
	"herdmux/internal/tmuxtest"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnvFailsClosedWithoutTMUX(t *testing.T) {
	t.Parallel()
	c, err := tmux.FromEnv(envOf(nil))
	if !errors.Is(err, tmux.ErrNoServer) || c != nil {
		t.Fatalf("FromEnv with no TMUX = %v, %v; want ErrNoServer", c, err)
	}
}

func TestRefusesNonTestServersInTests(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"/tmp/tmux-1000/default,123,0", "/tmp/tmux-1000/herdmux-dev,1,0"} {
		if _, err := tmux.FromEnv(envOf(map[string]string{"TMUX": v})); err == nil {
			t.Errorf("FromEnv(TMUX=%s) succeeded in a test binary", v)
		}
	}
	for _, name := range []string{"", "default", "herdmux-dev", "a/b"} {
		if _, err := tmux.OnSocket(name); err == nil {
			t.Errorf("OnSocket(%q) succeeded", name)
		}
	}
	if c, err := tmux.Default(); !errors.Is(err, tmux.ErrDefaultInTest) || c != nil {
		t.Errorf("Default() in a test binary = %v, %v; want ErrDefaultInTest", c, err)
	}
}

func TestClientsReachOnlyTheTestServer(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "@marker", s.Name)

	viaSocket, err := tmux.OnSocket(s.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := viaSocket.Display("", "#{@marker}"); err != nil || got != s.Name {
		t.Fatalf("OnSocket display = %q, %v", got, err)
	}
	if args := viaSocket.Args("ls"); strings.Join(args, " ") != "-L "+s.Name+" ls" {
		t.Fatalf("OnSocket args = %q", args)
	}

	tmuxVar := s.SocketPath + "," + s.PID + ",0"
	viaEnv, err := tmux.FromEnv(envOf(map[string]string{"TMUX": tmuxVar}))
	if err != nil {
		t.Fatal(err)
	}
	if args := viaEnv.Args("ls"); strings.Join(args, " ") != "ls" {
		t.Fatalf("FromEnv passes socket flags: %q", args)
	}
	if got, err := viaEnv.Display("", "#{@marker}"); err != nil || got != s.Name {
		t.Fatalf("FromEnv display = %q, %v", got, err)
	}
}

func TestRunErrorCarriesStderr(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	_, err := s.Client.Run("has-session", "-t", "nosuch")
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("err = %v", err)
	}
}

func TestFields(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	line := s.Display(s.Pane, tmux.Fields("#{pane_id}", "#{session_name}"))
	if f := tmux.Split(line); len(f) != 2 || f[0] != s.Pane || f[1] != "main" {
		t.Fatalf("fields = %q", f)
	}
	if l := tmux.Lines("a\nb\n"); len(l) != 2 {
		t.Fatalf("Lines = %q", l)
	}
}

func TestLiteral(t *testing.T) {
	for in, want := range map[string]string{"a": "a", ";": `\;`, "echo hi;": `echo hi\;`, "a;b": "a;b"} {
		if got := tmux.Literal(in); got != want {
			t.Errorf("Literal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatLiteral(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "x#(cmd)": "x##(cmd)", "#{pane_id};": `##{pane_id}\;`, "##": "####"} {
		if got := tmux.FormatLiteral(in); got != want {
			t.Errorf("FormatLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}
