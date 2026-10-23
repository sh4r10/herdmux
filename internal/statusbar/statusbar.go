// Package statusbar draws herdmux's whole status bar: the session name, the
// window tabs with agent-state icons and Attention fills, and the active
// pane's directory and git status on the right.
//
// The formats are pure tmux formats over pane options (format.go). The only
// thing herdmux computes is git status, stored per pane in the @herdmux_git_*
// options; status-right reads the active pane's, so it follows the active
// pane. Git is refreshed for a pane when tmux reports a pane, window or
// session change (GitHooks), and for every session's active pane by a
// periodic recheck (a `cd` fires no hook).
package statusbar

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/git"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// The git pane options. Empty head means nothing is shown.
const (
	OptGitHead  = "@herdmux_git_head"  // branch, or the short sha when detached
	OptGitDirty = "@herdmux_git_dirty" // "1" when anything is uncommitted
	OptGitStale = "@herdmux_git_stale" // "1" when the last git run timed out
)

// OptRecheck (global) holds the generation of the running recheck loop. Each
// init sets a new one; an older loop sees the change and exits.
const OptRecheck = "@herdmux_recheck"

// RecheckInterval is how often every session's active pane gets a git
// recheck (plus a reconcile).
const RecheckInterval = 3 * time.Second

// HookIndex is the hook array slot herdmux owns for the git hooks (the Visit
// hook is state.FocusHookIndex, 77).
const HookIndex = 78

// GitHooks fire when a pane, window or session becomes active; in each,
// #{pane_id} is the newly active pane. window-pane-changed and
// session-window-changed also cover split-window, new-window and kill-pane.
var GitHooks = []string{"window-pane-changed", "session-window-changed", "client-session-changed"}

// GitHookCommand is what the git hooks run.
const GitHookCommand = `run-shell -b "herdmux tmux-hook git-refresh #{pane_id}"`

// MouseKey is the root-table mouse key a click on status-right binds to:
// `herdmux next-attention`, so a click on the Attention-elsewhere indicator
// jumps there (tmux leaves this key unbound). It replaces a tmux.conf
// binding of the same key.
const MouseKey = "MouseDown1StatusRight"

// Init is this package's part of `herdmux init`: the status formats, the
// status-right mouse binding, the git hooks and the recheck loop, then a
// first recheck. It is idempotent: the hooks sit in fixed slots and a re-run
// replaces the recheck loop.
func Init(c *tmux.Client, home string) error {
	return initWith(c, home, RecheckInterval)
}

func initWith(c *tmux.Client, home string, interval time.Duration) error {
	gen := strconv.FormatInt(time.Now().UnixNano(), 36)
	sets := [][]string{
		{"set-option", "-g", "status-left", StatusLeft()},
		{"set-option", "-g", "status-left-length", "40"},
		{"set-option", "-g", "status-right", StatusRight(home)},
		{"set-option", "-g", "status-right-length", "80"},
		{"set-option", "-g", "status-justify", "left"},
		{"set-option", "-gw", "window-status-format", TabFormat(false)},
		{"set-option", "-gw", "window-status-current-format", TabFormat(true)},
		{"set-option", "-gw", "window-status-separator", ""},
		{"bind-key", "-N", "herdmux: jump to the next agent that needs attention", "-T", "root", MouseKey,
			"run-shell", "-b", config.BindingCommand("next-attention")},
		{"set-option", "-g", OptRecheck, gen},
		{"run-shell", "-b", recheckLoop(gen, interval)},
	}
	for _, h := range GitHooks {
		sets = append(sets, []string{"set-hook", "-g", fmt.Sprintf("%s[%d]", h, HookIndex), GitHookCommand})
	}
	if _, err := c.Run(join(sets)...); err != nil {
		return err
	}
	return Recheck(c)
}

// recheckLoop is a tmux-side timer: a run-shell -b sleep loop that runs
// `herdmux tmux-hook recheck` until the server dies (tmux show fails) or a
// later init replaces it (the generation changed). herdmux itself never
// stays running.
func recheckLoop(gen string, interval time.Duration) string {
	return fmt.Sprintf(`g=%s; while sleep %s && [ "$(tmux show-options -gqv %s)" = "$g" ]; do herdmux tmux-hook recheck; done`,
		gen, strconv.FormatFloat(interval.Seconds(), 'f', -1, 64), OptRecheck)
}

// paneFields is what a git refresh reads per pane.
var paneFields = tmux.Fields("#{pane_id}", "#{pane_current_path}",
	"#{"+OptGitHead+"}", "#{"+OptGitDirty+"}", "#{"+OptGitStale+"}")

type pane struct {
	id, path string
	git      git.Status
}

func parsePane(line string) (pane, bool) {
	f := tmux.Split(line)
	if len(f) != 5 {
		return pane{}, false
	}
	return pane{id: f[0], path: f[1], git: git.Status{Head: f[2], Dirty: f[3] == "1", Stale: f[4] == "1"}}, true
}

// Refresh recomputes git status for one pane (%N), from the git hooks.
func Refresh(c *tmux.Client, paneID string) error {
	out, err := c.Display(paneID, paneFields)
	if err != nil {
		return err
	}
	// display -t of a missing pane succeeds with an empty context.
	p, ok := parsePane(out)
	if !ok || p.id != paneID {
		return fmt.Errorf("no pane %q", paneID)
	}
	return update(c, []pane{p})
}

// Recheck recomputes git status for the active pane of every session's
// current window (a `cd` fires no hook, so this catches it).
func Recheck(c *tmux.Client) error {
	out, err := c.RunList("list-panes", "-a", "-f", "#{&&:#{window_active},#{pane_active}}", "-F", paneFields)
	if err != nil {
		return err
	}
	var ps []pane
	seen := map[string]bool{}
	for _, line := range tmux.Lines(out) {
		if p, ok := parsePane(line); ok && !seen[p.id] {
			seen[p.id] = true // a window linked into two sessions lists twice
			ps = append(ps, p)
		}
	}
	return update(c, ps)
}

// update runs git once per directory (concurrently) and writes only the
// options that changed, in one tmux call. Each pane keeps its own last value
// on a timeout.
func update(c *tmux.Client, ps []pane) error {
	type result struct {
		st  git.Status
		err error
	}
	var wg sync.WaitGroup
	byPath := map[string]*result{}
	for _, p := range ps {
		if _, ok := byPath[p.path]; ok {
			continue
		}
		r := &result{}
		byPath[p.path] = r
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			r.st, r.err = git.Read(path, git.Timeout)
		}(p.path)
	}
	wg.Wait()

	var cmds [][]string
	for _, p := range ps {
		r := byPath[p.path]
		cmds = append(cmds, diff(p.id, p.git, git.Merge(p.git, r.st, r.err))...)
	}
	return run(c, cmds)
}

func diff(id string, old, next git.Status) [][]string {
	var cmds [][]string
	put := func(name, was, now string) {
		switch {
		case was == now:
		case now == "":
			cmds = append(cmds, []string{"set-option", "-p", "-u", "-t", id, name})
		default:
			cmds = append(cmds, []string{"set-option", "-p", "-t", id, name, now})
		}
	}
	put(OptGitHead, old.Head, next.Head)
	put(OptGitDirty, flag(old.Dirty), flag(next.Dirty))
	put(OptGitStale, flag(old.Stale), flag(next.Stale))
	return cmds
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// run sends cmds as one tmux call; if that fails (a pane vanished, which
// aborts the rest of the sequence) each is retried alone.
func run(c *tmux.Client, cmds [][]string) error {
	if len(cmds) == 0 {
		return nil
	}
	if _, err := c.Run(join(cmds)...); err == nil || len(cmds) == 1 {
		return err
	}
	var errs []error
	for _, cmd := range cmds {
		if _, err := c.Run(cmd...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// join chains tmux commands with ";" arguments.
func join(cmds [][]string) []string {
	var args []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, cmd...)
	}
	return args
}
