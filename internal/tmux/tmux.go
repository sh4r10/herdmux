// Package tmux is herdmux's only way to run tmux commands.
//
// A Client talks to exactly one server, chosen when it is made:
//
//   - FromEnv: the server named by $TMUX (what every herdmux verb uses). The
//     tmux child process gets that TMUX value; no socket flag is passed.
//   - OnSocket: an explicit `-L <name>` server (tests and the dev harness).
//   - Default: tmux's default server, for one read-only check only (install's
//     preflight reads its global PATH when run outside tmux). It is refused
//     inside a test binary.
//
// It fails closed: FromEnv and OnSocket never fall through to tmux's default
// server. `-S` and `-L default` are never passed, and inside a test binary
// only `herdmux-test-*` servers are accepted.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSocketPrefix is the socket-name prefix every test server must use.
const TestSocketPrefix = "herdmux-test-"

// ErrNoServer is returned when no server is configured: $TMUX is unset and no
// socket name was given.
var ErrNoServer = errors.New("tmux: no server configured ($TMUX is unset)")

// Client runs tmux commands against one server.
type Client struct {
	socket  string // -L name; empty means the server in tmuxEnv
	tmuxEnv string // the TMUX value handed to the child (FromEnv only)
}

// FromEnv returns a Client for the server in $TMUX, as read through getenv.
// It returns ErrNoServer when TMUX is empty.
func FromEnv(getenv func(string) string) (*Client, error) {
	v := getenv("TMUX")
	if v == "" {
		return nil, ErrNoServer
	}
	sock, _, _ := strings.Cut(v, ",")
	if err := checkName(filepath.Base(sock)); err != nil {
		return nil, err
	}
	return &Client{tmuxEnv: v}, nil
}

// OnSocket returns a Client that passes `-L name` on every call.
func OnSocket(name string) (*Client, error) {
	if name == "" || name == "default" || strings.ContainsRune(name, '/') {
		return nil, fmt.Errorf("tmux: invalid socket name %q", name)
	}
	if err := checkName(name); err != nil {
		return nil, err
	}
	return &Client{socket: name}, nil
}

// ErrDefaultInTest is Default's error inside a test binary.
var ErrDefaultInTest = errors.New("tmux: test binaries may not use the default server")

// Default returns a Client for tmux's default server (no socket flag, TMUX
// removed from the child's environment). Its one user is `herdmux install`'s
// preflight run outside tmux, which reads the server's global PATH. Inside a
// test binary it returns ErrDefaultInTest.
func Default() (*Client, error) {
	if testing.Testing() {
		return nil, ErrDefaultInTest
	}
	return &Client{}, nil
}

// checkName enforces the isolation rules on a socket name. Inside a test
// binary only herdmux-test-* servers are allowed, so a test can never reach
// the user's server (not even through an inherited $TMUX).
func checkName(name string) error {
	if testing.Testing() && !strings.HasPrefix(name, TestSocketPrefix) {
		return fmt.Errorf("tmux: test binaries may only use %s* servers, not %q", TestSocketPrefix, name)
	}
	return nil
}

// Socket returns the -L name, or "" for a $TMUX client.
func (c *Client) Socket() string { return c.socket }

// Args returns the full argument list (after "tmux") for a call.
func (c *Client) Args(args ...string) []string {
	if c.socket == "" {
		return args
	}
	return append([]string{"-L", c.socket}, args...)
}

// Command returns an *exec.Cmd for `tmux <args>` aimed at this server.
func (c *Client) Command(args ...string) *exec.Cmd {
	cmd := exec.Command("tmux", c.Args(args...)...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			env = append(env, kv)
		}
	}
	if c.tmuxEnv != "" {
		env = append(env, "TMUX="+c.tmuxEnv)
	}
	cmd.Env = env
	return cmd
}

// Run runs `tmux <args>` and returns stdout without its trailing newline.
// A non-zero exit becomes an error carrying tmux's stderr.
func (c *Client) Run(args ...string) (string, error) {
	cmd := c.Command(args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// RunList is Run for the server-wide listings (list-panes -a,
// list-clients). tmux 3.5 fails those with "no current target" while the
// server has no session, which is when tmux.conf's run-shell 'herdmux init'
// runs at server start; RunList returns an empty listing instead.
func (c *Client) RunList(args ...string) (string, error) {
	out, err := c.Run(args...)
	if err != nil && strings.HasSuffix(err.Error(), ": no current target") {
		return "", nil
	}
	return out, err
}

// Display expands format with `display-message -p`, against target if it is
// not empty.
func (c *Client) Display(target, format string) (string, error) {
	if target == "" {
		return c.Run("display-message", "-p", format)
	}
	return c.Run("display-message", "-p", "-t", target, format)
}

// Lines splits tmux output into lines, dropping a trailing empty line.
func Lines(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

// FieldSep separates fields in herdmux's -F formats. tmux prints a tab as is
// (control characters such as \x1f come out escaped as "\037").
const FieldSep = "\t"

// Fields joins format variables with FieldSep, e.g.
// Fields("#{pane_id}", "#{@herdmux_state}").
func Fields(vars ...string) string { return strings.Join(vars, FieldSep) }

// Split splits one line of a Fields format.
func Split(line string) []string { return strings.Split(line, FieldSep) }

// Literal escapes an argument that tmux would read as a command separator:
// tmux takes a lone ";", or a ";" ending an argument, as one ("x;" arrives
// as "x"). Pass user-supplied text (typed commands, key names) through it.
func Literal(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}

// FormatLiteral escapes user text for an argument tmux expands as a format
// (new-session -s/-c, new-window -n/-c, rename-window, display-message): it
// doubles "#", so "#(cmd)" can't run a shell command and "#{...}" stays as
// typed, then applies Literal.
func FormatLiteral(s string) string { return Literal(strings.ReplaceAll(s, "#", "##")) }
