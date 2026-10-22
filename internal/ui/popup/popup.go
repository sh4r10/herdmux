// Package popup is the shared plumbing of herdmux's popups: a popup verb
// (picker, organiser, colorscheme, launch) opens its own display-popup and
// re-runs itself inside it, where HERDMUX_POPUP=1 tells it to run the
// bubbletea program instead. Key bindings stay one-liners and popup sizes
// live in Go.
//
// The usual shape of a popup verb:
//
//	client, err := popup.Client(c, e.getenv, *clientFlag)
//	if !popup.Inside(e.getenv) {
//		return popup.Open(c, client, popup.Large, "picker")
//	}
//	m, err := popup.Run(model)     // inside the popup; HERDMUX_CLIENT is the client
//	// on error: popup.ShowError(c, client, err.Error())
package popup

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"herdmux/internal/colorscheme"
	"herdmux/internal/tmux"
)

// Environment a popup's inner run gets.
const (
	EnvPopup  = "HERDMUX_POPUP"  // "1" inside a herdmux popup
	EnvClient = "HERDMUX_CLIENT" // the client the popup is shown on
)

// Size is a display-popup size: cells ("60") or a percentage of the client
// ("85%").
type Size struct{ W, H string }

// Large is the split popups' size (picker, organiser, colorscheme picker).
var Large = Size{W: "85%", H: "75%"}

// Cells is a size in cells.
func Cells(w, h int) Size { return Size{W: fmt.Sprint(w), H: fmt.Sprint(h)} }

// Inside reports whether this run is a popup's inner run.
func Inside(getenv func(string) string) bool { return getenv(EnvPopup) == "1" }

// Executable is the binary a popup re-runs (this one).
var Executable = os.Executable

// Open opens a popup on client that runs `herdmux <args>` with
// HERDMUX_POPUP=1 and HERDMUX_CLIENT=<client>, and returns without waiting
// for it: display-popup -E blocks its caller until the popup closes. The
// popup has no tmux border (-B); the program draws its own with
// style.Styles.Frame, so the border follows the colorscheme. client must
// come from Client, which has checked it (Open makes no tmux call of its
// own before display-popup: every call delays the popup).
func Open(c *tmux.Client, client string, size Size, args ...string) error {
	if client == "" {
		return errors.New("no client to open the popup on")
	}
	exe, err := Executable()
	if err != nil {
		return err
	}
	words := []string{shellQuote(exe)}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	cmd := c.Command("display-popup", "-c", client, "-E", "-B", "-w", size.W, "-h", size.H,
		"-e", EnvPopup+"=1", "-e", EnvClient+"="+client, strings.Join(words, " "))
	// No stdio: a run-shell -b job would otherwise wait for the popup too.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reaped if this process lives on (tests)
	return nil
}

// Client resolves the client a popup verb acts for:
//
//  1. the --client flag (key bindings pass '#{client_name}'), which must exist;
//  2. HERDMUX_CLIENT, inside a popup;
//  3. run from a pane ($TMUX_PANE): the most recently active client showing
//     that pane's session.
func Client(c *tmux.Client, getenv func(string) string, flag string) (string, error) {
	if flag == "" {
		flag = getenv(EnvClient)
	}
	if flag != "" {
		return flag, checkClient(c, flag)
	}
	pane := getenv("TMUX_PANE")
	if pane == "" {
		return "", errors.New("no client: run it from a tmux pane or pass --client")
	}
	sess, err := c.Display(pane, "#{session_id}")
	if err != nil {
		return "", err
	}
	out, err := c.Run("list-clients", "-F", tmux.Fields("#{client_name}", "#{session_id}", "#{client_activity}"))
	if err != nil {
		return "", err
	}
	best, bestAct := "", ""
	for _, line := range tmux.Lines(out) {
		f := tmux.Split(line)
		if len(f) == 3 && f[1] == sess && (best == "" || longer(f[2], bestAct)) {
			best, bestAct = f[0], f[2]
		}
	}
	if best == "" {
		return "", errors.New("no client is showing this session")
	}
	return best, nil
}

// checkClient fails unless client is attached (display -c quietly falls
// back to another client for an unknown name).
func checkClient(c *tmux.Client, client string) error {
	out, err := c.Run("list-clients", "-F", "#{client_name}")
	if err != nil {
		return err
	}
	if !slices.Contains(tmux.Lines(out), client) {
		return fmt.Errorf("no client %q", client)
	}
	return nil
}

// longer compares two decimal timestamps.
func longer(a, b string) bool { return len(a) > len(b) || (len(a) == len(b) && a > b) }

// ShowError shows msg in red (attention_needs_input) on client ("" lets
// tmux pick) for 8 s, through tmux's message style. It is the one error
// display for popup verbs and a broken config.toml. Errors are ignored (no
// client yet, e.g. during the first init).
func ShowError(c *tmux.Client, client, msg string) {
	red := colorscheme.Ref("attention_needs_input")
	text := "#[fg=#{?" + red + "," + red + ",red}]" + tmux.FormatLiteral(msg)
	args := []string{"display-message", "-d", "8000"}
	if client != "" {
		args = append(args, "-c", client)
	}
	_, _ = c.Run(append(args, text)...)
}

// Run runs a popup's bubbletea program full-screen in the popup and returns
// the final model.
func Run(m tea.Model) (tea.Model, error) {
	return tea.NewProgram(m, tea.WithAltScreen()).Run()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
