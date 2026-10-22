package sessionizer

import (
	"herdmux/internal/config"
	"herdmux/internal/sessionizer"
	"herdmux/internal/tmux"
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
