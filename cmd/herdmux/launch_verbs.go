package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/launch"
	"github.com/sh4r10/herdmux/internal/logfile"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/launchprompt"
	"github.com/sh4r10/herdmux/internal/ui/popup"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// config.toml, [keys] and launch.

// clientFlag declares --client, which key bindings pass as '#{client_name}'
// (config.BindingCommand). Every verb a [keys] action runs has it.
func clientFlag(fs *flag.FlagSet) *string {
	return fs.String("client", "", "the tmux client to act for (key bindings pass '#{client_name}')")
}

// showOnClient is for verbs run from a key binding or inside a popup, where
// nobody sees stderr: the error is shown in red on the client and logged,
// and the verb exits 1 quietly.
func showOnClient(e *env, c *tmux.Client, client, verb string, err error) error {
	var ue usageError
	if err == nil || client == "" || errors.As(err, &ue) {
		return err
	}
	msg := "herdmux " + verb + ": " + err.Error()
	popup.ShowError(c, client, msg)
	_ = logfile.Printf(e.getenv, "%s", msg)
	return shownError{err}
}

// initKeys is init's [keys] step: bind the keys from config.toml (the
// defaults if it is broken). A broken file or a key tmux refuses is shown
// in red and logged (at server start no client sees the message), and init
// carries on.
func initKeys(e *env, c *tmux.Client) {
	cfg, cfgErr := config.Load(e.getenv)
	for _, err := range []error{cfgErr, config.Bind(c, cfg.Keys)} {
		if err != nil {
			popup.ShowError(c, "", "herdmux: "+err.Error())
			_ = logfile.Printf(e.getenv, "init: %v", err)
		}
	}
}

// defineLaunch: `launch <name>` launches that [[agent]] entry; `launch`
// opens the launch prompt popup, whose inner run (HERDMUX_POPUP=1) shows the
// prompt and launches the chosen agent.
func defineLaunch(fs *flag.FlagSet) func(*env, []string) error {
	clientName := clientFlag(fs)
	return func(e *env, args []string) error {
		if len(args) > 1 {
			return usagef("want at most one agent name")
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		inPopup := popup.Inside(e.getenv)
		client, clientErr := popup.Client(c, e.getenv, *clientName)
		shown := ""
		if *clientName != "" || inPopup {
			if clientErr != nil {
				return clientErr
			}
			shown = client
		}
		cfg, cfgErr := config.Load(e.getenv)

		if len(args) == 1 {
			a, ok := cfg.Agent(args[0])
			if !ok {
				err := fmt.Errorf("no agent %q in %s", args[0], config.FileName)
				if cfgErr != nil {
					err = fmt.Errorf("no agent %q (%v)", args[0], cfgErr)
				}
				return showOnClient(e, c, shown, "launch", err)
			}
			var t launch.Target
			if shown != "" {
				t, err = launch.ClientTarget(c, client)
			} else {
				t, err = launch.PaneTarget(c, e.getenv("TMUX_PANE"), client) // client may be "": no switch
			}
			if err == nil {
				_, err = launch.Launch(c, a, t)
			}
			return showOnClient(e, c, shown, "launch", err)
		}

		if clientErr != nil {
			return clientErr
		}
		p := launchprompt.Prompt{Agents: cfg.Visible(e.getenv("PATH")), Err: cfgErr}
		if !inPopup {
			w, h := p.Size()
			return showOnClient(e, c, shown, "launch", popup.Open(c, client, popup.Cells(w, h), "launch"))
		}
		return showOnClient(e, c, client, "launch", runLaunchPrompt(e, c, client, p))
	}
}

func runLaunchPrompt(e *env, c *tmux.Client, client string, p launchprompt.Prompt) error {
	t, err := launch.ClientTarget(c, client)
	if err != nil {
		return err
	}
	p.Where = launchprompt.ShortDir(t.Dir, e.getenv("HOME"))
	pal, err := style.LoadPalette(c)
	if err != nil {
		return err
	}
	m, err := popup.Run(launchprompt.New(p, style.New(pal)))
	if err != nil {
		return err
	}
	if a, ok := m.(launchprompt.Model).Chosen(); ok {
		_, err = launch.Launch(c, a, t)
	}
	return err
}
