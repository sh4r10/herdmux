package main

import (
	"flag"

	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/launch"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/launchprompt"
	"github.com/sh4r10/herdmux/internal/ui/picker"
	"github.com/sh4r10/herdmux/internal/ui/popup"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// The agent picker popup.

// definePicker: outside a popup, `picker` opens the 85% × 75% popup on the
// client; its inner run (HERDMUX_POPUP=1) shows the picker, then carries out
// the jump or launch chosen in it.
func definePicker(fs *flag.FlagSet) func(*env, []string) error {
	clientName := clientFlag(fs)
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("picker takes no arguments")
		}
		if !popup.Inside(e.getenv) {
			return openPopup(e, *clientName, popup.Large, "picker")
		}
		c, err := tmux.FromEnv(e.getenv)
		if err != nil {
			return err
		}
		return runPicker(e, c, *clientName)
	}
}

// runPicker is the inner run. Reconcile and the reads the first frame needs
// run at once (see parallel).
func runPicker(e *env, c *tmux.Client, clientName string) error {
	var (
		client, session string
		pal             style.Palette
		set             picker.Settings
	)
	err := parallel(
		func() error { return state.Reconcile(c) },
		func() (err error) {
			if client, err = popup.Client(c, e.getenv, clientName); err != nil {
				client = ""
				return err
			}
			session, err = picker.ClientSession(c, client)
			return err
		},
		func() (err error) { pal, err = style.LoadPalette(c); return err },
		func() (err error) { set, err = picker.LoadSettings(c); return err },
	)
	if err != nil {
		return showOnClient(e, c, client, "picker", err)
	}
	cfg, cfgErr := config.Load(e.getenv) // read on every open
	m, err := popup.Run(picker.New(picker.Options{
		Backend:  picker.TmuxBackend{C: c},
		Styles:   style.New(pal),
		Session:  session,
		Settings: set,
		Prompt:   launchprompt.Prompt{Agents: cfg.Visible(e.getenv("PATH")), Err: cfgErr},
	}))
	if err != nil {
		return showOnClient(e, c, client, "picker", err)
	}
	return showOnClient(e, c, client, "picker", pickerResult(c, client, m.(picker.Model).Result()))
}

// pickerResult carries out the jump or launch chosen in the picker.
func pickerResult(c *tmux.Client, client string, res picker.Result) error {
	switch {
	case res.Jump != nil:
		return launch.Switch(c, client, res.Jump.Pane)
	case res.Launch != nil:
		t, err := launch.ClientTarget(c, client)
		if err != nil {
			return err
		}
		_, err = launch.Launch(c, *res.Launch, t)
		return err
	}
	return nil
}
