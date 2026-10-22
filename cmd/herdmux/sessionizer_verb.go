package main

import (
	"flag"

	"herdmux/internal/config"
	"herdmux/internal/sessionizer"
	"herdmux/internal/tmux"
	"herdmux/internal/ui/popup"
	usessionizer "herdmux/internal/ui/sessionizer"
	"herdmux/internal/ui/style"
)

// The session switcher / project launcher popup (prefix g by default).

// defineSessionizer: outside a popup, `sessionizer` opens the popup on the
// client; its inner run (HERDMUX_POPUP=1) shows the sessionizer, then carries
// out the switch chosen in it.
func defineSessionizer(fs *flag.FlagSet) func(*env, []string) error {
	clientName := clientFlag(fs)
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("sessionizer takes no arguments")
		}
		if !popup.Inside(e.getenv) {
			return openPopup(e, *clientName, popup.Large, "sessionizer")
		}
		c, err := tmux.FromEnv(e.getenv)
		if err != nil {
			return err
		}
		return runSessionizer(e, c, *clientName)
	}
}

// runSessionizer is the inner run. Load config and the reads the first frame needs
// run at once (see parallel).
func runSessionizer(e *env, c *tmux.Client, clientName string) error {
	var (
		client string
		pal    style.Palette
	)
	err := parallel(
		func() (err error) {
			if client, err = popup.Client(c, e.getenv, clientName); err != nil {
				client = ""
				return err
			}
			return nil
		},
		func() (err error) { pal, err = style.LoadPalette(c); return err },
	)
	if err != nil {
		return showOnClient(e, c, client, "sessionizer", err)
	}

	cfg, _ := config.Load(e.getenv)
	m, err := popup.Run(usessionizer.New(usessionizer.Options{
		Backend: usessionizer.TmuxBackend{C: c, Cfg: cfg.Sessionizer},
		Styles:  style.New(pal),
	}))
	if err != nil {
		return showOnClient(e, c, client, "sessionizer", err)
	}

	// Carry out the switch chosen in the popup.
	if res := m.(usessionizer.Model).Result(); res.Switch != nil {
		err = sessionizer.SwitchTo(c, client, *res.Switch)
	}
	return showOnClient(e, c, client, "sessionizer", err)
}
