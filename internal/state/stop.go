package state

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"herdmux/internal/tmux"
)

// StopWait is how long Stop waits for the agent to leave the foreground.
const StopWait = 1500 * time.Millisecond

// ErrPaneProcess is Stop's refusal when the pane's own process is in the
// foreground (an idle shell, or an agent started as the pane's command with
// no shell under it): stopping it would close the pane.
var ErrPaneProcess = errors.New("no process runs in the foreground of the pane's shell (stopping the pane's own process would close it)")

// Stop stops the agent in pane and keeps the pane: it
// sends SIGTERM to the foreground process group of the pane's tty, so the
// agent (and any children in its group) exits and the pane's shell comes
// back. It refuses when that group is the pane's own process
// (ErrPaneProcess). It waits up to StopWait for the group to leave the
// foreground, then reconciles, which unsets the agent.
//
// The foreground group is used rather than @herdmux_pid because it is what
// runs in the pane right now (wrapper scripts and node children included),
// and it is never the pane's shell.
func Stop(c *tmux.Client, pane string) error {
	if err := checkPane(pane); err != nil {
		return err
	}
	out, err := c.Display(pane, tmux.Fields("#{pane_id}", "#{pane_pid}"))
	if err != nil {
		return err
	}
	f := tmux.Split(out)
	if len(f) != 2 || f[0] != pane { // display -t on a missing pane succeeds with an empty context
		return fmt.Errorf("no pane %q", pane)
	}
	panePID := atoi(f[1])
	fg, err := foregroundPgrp(panePID)
	if err != nil {
		return err
	}
	if fg <= 1 || fg == panePID {
		return ErrPaneProcess
	}
	if err := syscall.Kill(-fg, syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM to process group %d: %w", fg, err)
	}
	gone := false
	for deadline := time.Now().Add(StopWait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if g, err := foregroundPgrp(panePID); err != nil || g != fg {
			gone = true
			break
		}
	}
	if err := Reconcile(c); err != nil {
		return err
	}
	if !gone {
		return errors.New("still running after SIGTERM")
	}
	return nil
}
