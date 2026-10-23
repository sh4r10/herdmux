package tmuxtest

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sh4r10/herdmux/internal/tmux"
)

// Client is a real attached client: `tmux attach` running under script(1) in
// a 200x50 pty, with its terminal input fed through a pipe. Everything tmux
// draws is kept in Output, so tests can count BEL bytes and similar.
type Client struct {
	s    *Server
	TTY  string // #{client_tty}; use it as a -t/-c target
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *lockedBuffer
	once sync.Once
}

// Attach attaches a new client to session and waits until the server lists
// it. The client is detached and its process reaped in t.Cleanup. Attach
// calls on one server must not run concurrently.
func (s *Server) Attach(session string) *Client {
	s.t.Helper()
	before := s.clientTTYs()
	attach := fmt.Sprintf("stty cols 200 rows 50; TERM=xterm-256color exec tmux -L %s attach -t %s",
		s.Name, shellQuote(session))
	cmd := exec.Command("script", "-qfec", attach, "/dev/null")
	cmd.Env = s.OutsideEnv()
	cmd.Dir = s.Home
	in, err := cmd.StdinPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	c := &Client{s: s, cmd: cmd, in: in, out: &lockedBuffer{}}
	cmd.Stdout, cmd.Stderr = c.out, c.out
	if err := cmd.Start(); err != nil {
		s.t.Fatalf("tmuxtest: starting attached client: %v", err)
	}
	s.t.Cleanup(c.Close)
	s.Eventually(5*time.Second, "the client to attach to "+session, func() bool {
		for _, tty := range s.clientTTYs() {
			if !slices.Contains(before, tty) {
				c.TTY = tty
				return true
			}
		}
		return false
	})
	return c
}

func (s *Server) clientTTYs() []string {
	out, _ := s.Client.Run("list-clients", "-F", "#{client_tty}")
	return tmux.Lines(out)
}

// Write sends raw bytes as if typed into the client's terminal.
func (c *Client) Write(b string) {
	c.s.t.Helper()
	if _, err := io.WriteString(c.in, b); err != nil {
		c.s.t.Fatalf("tmuxtest: writing to client: %v", err)
	}
}

// FocusOut sends the terminal focus-out report (\e[O), as when the Windows
// Terminal tab loses focus. It needs focus-events on.
func (c *Client) FocusOut() { c.Write("\x1b[O") }

// FocusIn sends the terminal focus-in report (\e[I).
func (c *Client) FocusIn() { c.Write("\x1b[I") }

// Format expands format for this client (display -p -c <tty>).
func (c *Client) Format(format string) string {
	c.s.t.Helper()
	return c.s.Tmux("display-message", "-p", "-c", c.TTY, format)
}

// Output returns everything the client's terminal has received so far.
func (c *Client) Output() string { return c.out.String() }

// Close detaches the client and waits for its process to exit. It is safe to
// call more than once.
func (c *Client) Close() {
	c.once.Do(func() {
		if c.TTY != "" {
			_, _ = c.s.Client.Run("detach-client", "-t", c.TTY)
		}
		_ = c.in.Close()
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
