package main

import (
	"github.com/sh4r10/herdmux/internal/attention"
	"github.com/sh4r10/herdmux/internal/statusbar"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// The status bar's parts of init and tmux-hook.

// initStatusbar applies the chosen colorscheme, then sets the
// status formats, the git hooks and the recheck loop.
func initStatusbar(e *env, c *tmux.Client) error {
	if err := initColorscheme(e, c); err != nil {
		return err
	}
	return statusbar.Init(c, e.getenv("HOME"))
}

// statusbarHook handles the git events of `tmux-hook` (already reconciled):
//
//	git-refresh <pane_id>   from the GitHooks: a pane, window or session became active
//	recheck                 from the recheck loop: every session's active pane,
//	                        and the Attention-elsewhere indicator (agents died)
//
// ok is false for any other event.
func statusbarHook(c *tmux.Client, ev string, args []string) (ok bool, err error) {
	switch ev {
	case "git-refresh":
		if len(args) != 1 {
			return true, usagef("git-refresh wants the pane id")
		}
		return true, statusbar.Refresh(c, args[0])
	case "recheck":
		if len(args) != 0 {
			return true, usagef("recheck takes no arguments")
		}
		if err := statusbar.Recheck(c); err != nil {
			return true, err
		}
		return true, attention.Sync(c)
	}
	return false, nil
}
