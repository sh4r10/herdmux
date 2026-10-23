package split

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/tmux"
)

// Capture is the live preview's capture of one pane:
// the last n lines that matter, with colours (capture-pane -e). It is one
// tmux call that starts n lines above the pane's screen (reaching into
// history when there is some) and runs to the bottom of the screen; blank
// lines under the last written one are dropped, so the preview ends at about
// the pane's cursor. Capture only the selected pane, and only what the panel
// shows.
func Capture(c *tmux.Client, pane string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out, err := c.Run("capture-pane", "-p", "-e", "-t", pane, "-S", "-"+strconv.Itoa(n))
	if err != nil {
		return nil, err
	}
	return lastLines(out, n), nil
}

// lastLines drops trailing blank lines (escape codes don't count) and keeps
// the last n.
func lastLines(out string, n int) []string {
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
