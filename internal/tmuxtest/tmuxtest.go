// Package tmuxtest runs hermetic tests against throwaway tmux servers.
//
// New(t) starts `tmux -L herdmux-test-<random> -f /dev/null` with a scrubbed
// environment (no TMUX, TMUX_PANE or HERDR_*; HOME, XDG dirs and
// HERDMUX_CONFIG_DIR in t.TempDir(); SHELL=/bin/sh; the freshly built herdmux
// first on PATH) and kills it in t.Cleanup. Tests drive the server through
// real herdmux commands (Herdmux, Run) and inspect it with Tmux, Display,
// PaneOption and ListPanes. Attach adds a real attached client.
//
// Every helper fails the test on error; the *Err variants return it instead.
// Servers are independent, so tests using them may call t.Parallel.
package tmuxtest

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"herdmux/internal/tmux"
)

// Server is one throwaway tmux server.
type Server struct {
	t          testing.TB
	Name       string       // socket name, herdmux-test-<hex>
	SocketPath string       // #{socket_path}
	PID        string       // #{pid} of the server
	Pane       string       // the first pane, %0, in session "main"
	Home       string       // HOME (also holds the XDG dirs and the log)
	ConfigDir  string       // HERDMUX_CONFIG_DIR
	Client     *tmux.Client // an internal/tmux client on this server
	env        []string     // the scrubbed environment the server started with
}

// Session is the name of the session New creates.
const Session = "main"

// New starts a fresh server with one detached 200x50 session, "main".
func New(t testing.TB) *Server {
	t.Helper()
	var rb [8]byte
	_, _ = rand.Read(rb[:])
	name := tmux.TestSocketPrefix + hex.EncodeToString(rb[:])
	client, err := tmux.OnSocket(name)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	s := &Server{t: t, Name: name, Home: home, ConfigDir: filepath.Join(home, "herdmux-config"), Client: client}
	s.env = scrubbedEnv(home, s.ConfigDir, filepath.Dir(Binary(t)))

	cmd := exec.Command("tmux", client.Args("-f", "/dev/null", "new-session", "-d", "-s", Session, "-x", "200", "-y", "50")...)
	cmd.Env = s.env
	cmd.Dir = home
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmuxtest: starting server %s: %v: %s", name, err, out)
	}
	t.Cleanup(s.kill)
	info := strings.Split(s.Display(Session+":", "#{socket_path}\t#{pid}\t#{pane_id}"), "\t")
	s.SocketPath, s.PID, s.Pane = info[0], info[1], info[2]
	return s
}

// kill stops the server and removes its socket file (tmux 3.5a leaves the
// file behind after kill-server).
func (s *Server) kill() {
	_, _ = s.Client.Run("kill-server")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := s.Client.Run("list-sessions"); err != nil {
			break
		}
		if time.Now().After(deadline) {
			s.t.Errorf("tmuxtest: server %s still answers after kill-server", s.Name)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.Remove(s.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.t.Errorf("tmuxtest: removing socket: %v", err)
	}
}

// scrubbedEnv is os.Environ() minus everything that could point at the user's
// tmux server, herdr, or real config, plus the test's own HOME and dirs.
func scrubbedEnv(home, configDir, binDir string) []string {
	drop := []string{"TMUX=", "TMUX_PANE=", "HERDR_", "HERDMUX", "HOME=", "XDG_CONFIG_HOME=",
		"XDG_STATE_HOME=", "XDG_DATA_HOME=", "XDG_CACHE_HOME=", "SHELL=", "PATH="}
	var env []string
outer:
	for _, kv := range os.Environ() {
		for _, p := range drop {
			if strings.HasPrefix(kv, p) {
				continue outer
			}
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"HERDMUX_CONFIG_DIR="+configDir,
		"SHELL=/bin/sh",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
}

// Env returns the environment herdmux sees when run from pane inside this
// server: the server's scrubbed environment plus TMUX and TMUX_PANE.
func (s *Server) Env(pane string) []string {
	return append(append([]string(nil), s.env...),
		"TMUX="+s.SocketPath+","+s.PID+",0",
		"TMUX_PANE="+pane,
	)
}

// OutsideEnv returns the server's scrubbed environment with no TMUX at all,
// i.e. herdmux run from a shell outside tmux.
func (s *Server) OutsideEnv() []string { return append([]string(nil), s.env...) }

// Tmux runs a tmux command on the server and returns its stdout without the
// trailing newline. It fails the test on error.
func (s *Server) Tmux(args ...string) string {
	s.t.Helper()
	out, err := s.Client.Run(args...)
	if err != nil {
		s.t.Fatalf("tmuxtest: %v", err)
	}
	return out
}

// TmuxErr is Tmux returning the error instead.
func (s *Server) TmuxErr(args ...string) (string, error) { return s.Client.Run(args...) }

// Display expands format with display -p against target ("" for the
// server's default target).
func (s *Server) Display(target, format string) string {
	s.t.Helper()
	out, err := s.Client.Display(target, format)
	if err != nil {
		s.t.Fatalf("tmuxtest: %v", err)
	}
	return out
}

// PaneOption returns the value of a pane option (e.g. "@herdmux_state"), or
// "" when it is unset.
func (s *Server) PaneOption(pane, name string) string {
	s.t.Helper()
	return s.Tmux("show-options", "-pqv", "-t", pane, name)
}

// ListPanes returns one line per pane on the server (list-panes -a -F).
func (s *Server) ListPanes(format string) []string {
	s.t.Helper()
	return tmux.Lines(s.Tmux("list-panes", "-a", "-F", format))
}

// Cmd is one herdmux invocation for Run.
type Cmd struct {
	Args    []string
	Pane    string   // TMUX_PANE; default s.Pane
	Outside bool     // run with no TMUX/TMUX_PANE, as from outside tmux
	Stdin   string   // fed on stdin
	Env     []string // extra KEY=value entries, appended last (so they win)
}

// Result is a finished herdmux run.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Run runs the built herdmux binary as if from a pane of this server.
func (s *Server) Run(c Cmd) Result {
	s.t.Helper()
	cmd := exec.Command(Binary(s.t), c.Args...)
	switch {
	case c.Outside:
		cmd.Env = s.OutsideEnv()
	case c.Pane != "":
		cmd.Env = s.Env(c.Pane)
	default:
		cmd.Env = s.Env(s.Pane)
	}
	cmd.Env = append(cmd.Env, c.Env...)
	cmd.Dir = s.Home
	cmd.Stdin = strings.NewReader(c.Stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		r.Code = ee.ExitCode()
	case err != nil:
		s.t.Fatalf("tmuxtest: running herdmux %v: %v", c.Args, err)
	}
	return r
}

// Herdmux runs `herdmux <args>` from s.Pane.
func (s *Server) Herdmux(args ...string) Result {
	s.t.Helper()
	return s.Run(Cmd{Args: args})
}

// LogPath is herdmux's log file for runs against this server.
func (s *Server) LogPath() string {
	return filepath.Join(s.Home, ".local", "state", "herdmux", "log")
}

// Eventually polls cond every 10ms until it holds, failing the test after
// timeout. Use it for effects of `run-shell -b` hooks and other async work.
func (s *Server) Eventually(timeout time.Duration, what string, cond func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			s.t.Fatalf("tmuxtest: timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var build struct {
	once sync.Once
	path string
	err  error
}

// Binary returns the path of a herdmux binary built from this checkout, once
// per test process, into a per-user, per-checkout temp dir.
func Binary(t testing.TB) string {
	t.Helper()
	build.once.Do(func() { build.path, build.err = buildBinary() })
	if build.err != nil {
		t.Fatalf("tmuxtest: building herdmux: %v", build.err)
	}
	return build.path
}

func buildBinary() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	// One dir per checkout, so test runs in two worktrees never share a binary.
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("herdmux-tmuxtest-%d-%x", os.Getuid(), sum[:4]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Build to a unique name, then rename over the shared one, so parallel
	// test processes never write a binary another one is executing.
	tmp := filepath.Join(dir, fmt.Sprintf("herdmux.%d.tmp", os.Getpid()))
	cmd := exec.Command("go", "build", "-o", tmp, "./cmd/herdmux")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, out)
	}
	final := filepath.Join(dir, "herdmux")
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.HasPrefix(b, []byte("module herdmux\n")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod for module herdmux not found")
		}
		dir = parent
	}
}
