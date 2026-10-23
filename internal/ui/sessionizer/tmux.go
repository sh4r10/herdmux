package sessionizer

import (
	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/sessionizer"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/split"
)

// TmuxBackend is the Backend on a real server.
type TmuxBackend struct {
	C   *tmux.Client
	Cfg config.Sessionizer // roots and depth from config.toml
}

// Entries lists all sessions and directories.
func (b TmuxBackend) Entries() ([]sessionizer.Entry, error) {
	return sessionizer.List(b.C, b.Cfg.Roots, b.Cfg.Depth)
}

// Capture is the preview of a session's active pane (empty on error).
func (b TmuxBackend) Capture(pane string, n int) []string {
	lines, _ := split.Capture(b.C, pane, n)
	return lines
}
