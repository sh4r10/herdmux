package main

import (
	"flag"

	"herdmux/internal/state"
	"herdmux/internal/tmux"
	"herdmux/internal/ui/organiser"
	"herdmux/internal/ui/popup"
	"herdmux/internal/ui/style"
)

// The window organiser popup.

// defineOrganiser: outside a popup, `organiser` opens the 85% × 75% popup
// on the client; its inner run (HERDMUX_POPUP=1) shows the organiser for
// the client's session, then carries out the jump chosen in it. Moves,
// renames and kills happen live inside.
func defineOrganiser(fs *flag.FlagSet) func(*env, []string) error {
	clientName := clientFlag(fs)
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("organiser takes no arguments")
		}
		if !popup.Inside(e.getenv) {
			return openPopup(e, *clientName, popup.Large, "organiser")
		}
		c, err := tmux.FromEnv(e.getenv)
		if err != nil {
			return err
		}
		return runOrganiser(e, c, *clientName)
	}
}

// runOrganiser is the inner run. Reconcile and the reads the first frame
// needs run at once (see parallel).
func runOrganiser(e *env, c *tmux.Client, clientName string) error {
	var (
		client, id, name string
		pal              style.Palette
		b                organiser.TmuxBackend
	)
	err := parallel(
		func() error { return state.Reconcile(c) },
		func() (err error) {
			if client, err = popup.Client(c, e.getenv, clientName); err != nil {
				client = ""
				return err
			}
			id, name, err = organiser.ClientSession(c, client)
			return err
		},
		func() (err error) { pal, err = style.LoadPalette(c); return err },
		func() (err error) { b, err = organiser.NewTmuxBackend(c, ""); return err },
	)
	if err != nil {
		return showOnClient(e, c, client, "organiser", err)
	}
	b.Session = id
	m, err := popup.Run(organiser.New(organiser.Options{Backend: b, Styles: style.New(pal), Session: name}))
	if err == nil {
		if jump := m.(organiser.Model).Result().Jump; jump != "" {
			err = organiser.Select(c, id, jump)
		}
	}
	return showOnClient(e, c, client, "organiser", err)
}
