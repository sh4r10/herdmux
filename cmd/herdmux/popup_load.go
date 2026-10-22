package main

import (
	"sync"

	"herdmux/internal/tmux"
	"herdmux/internal/ui/popup"
)

// Popup verbs open fast by keeping tmux calls off the key-to-first-frame
// path: each call is a tmux client process (about 10-30 ms on WSL2).
//
//   - The opener (outside the popup) only resolves the client and runs
//     display-popup; it does not reconcile, since the inner run does.
//   - The inner run does its independent reads at once with parallel.

// openPopup is a popup verb's outer run: resolve the client and open the
// popup that re-runs `herdmux <verb>` inside.
func openPopup(e *env, clientName string, size popup.Size, verb string) error {
	c, err := tmux.FromEnv(e.getenv)
	if err != nil {
		return err
	}
	client, err := popup.Client(c, e.getenv, clientName)
	if err != nil {
		return err
	}
	return showOnClient(e, c, client, verb, popup.Open(c, client, size, verb))
}

// parallel runs fns at once, waits for all of them and returns the first
// error in argument order (nil if none).
func parallel(fns ...func() error) error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Go(func() { errs[i] = fn() })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
