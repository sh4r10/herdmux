package main

import (
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/logfile"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
	"github.com/sh4r10/herdmux/internal/ui/colorpicker"
	"github.com/sh4r10/herdmux/internal/ui/popup"
)

// The colorscheme parts of init and the colorscheme verb.

// initColorscheme applies the saved scheme, falling back to flexoki-dark with
// a tmux message (also logged) when it is unknown or invalid.
func initColorscheme(e *env, c *tmux.Client) error {
	st, err := colorscheme.FromEnv(e.getenv)
	if err != nil {
		return err
	}
	r, err := colorscheme.ApplySaved(c, st)
	if r.Warning != "" {
		_ = logfile.Printf(e.getenv, "init: %s", r.Warning)
	}
	return err
}

// defineColorscheme: `colorscheme --list` works anywhere; `colorscheme
// <name>` needs tmux (it applies live) and saves only if that worked; no
// argument opens the picker popup.
func defineColorscheme(fs *flag.FlagSet) func(*env, []string) error {
	list := fs.Bool("list", false, "list the available colorschemes")
	clientName := clientFlag(fs) // the picker's client
	return func(e *env, args []string) error {
		st, err := colorscheme.FromEnv(e.getenv)
		if err != nil {
			return err
		}
		if *list {
			if len(args) > 0 {
				return usagef("--list takes no name")
			}
			return listColorschemes(e, st)
		}
		if len(args) > 1 {
			return usagef("too many arguments")
		}
		if e.getenv("TMUX") == "" {
			return errNotInTmux
		}
		c, err := reconciled(e)
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return openColorpicker(e, c, st, *clientName)
		}
		if _, err := colorscheme.Set(c, st, args[0]); err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "colorscheme %s applied and saved\n", args[0])
		return nil
	}
}

// openColorpicker is the bare `colorscheme`: outside a
// popup it opens the 85% × 75% popup on the client; its inner run
// (HERDMUX_POPUP=1) shows the picker, then saves the choice or reverts to
// the saved scheme.
func openColorpicker(e *env, c *tmux.Client, st *colorscheme.Store, clientName string) error {
	client, err := popup.Client(c, e.getenv, clientName)
	if err != nil {
		return err
	}
	if !popup.Inside(e.getenv) {
		return showOnClient(e, c, client, "colorscheme", popup.Open(c, client, popup.Large, "colorscheme"))
	}
	return showOnClient(e, c, client, "colorscheme", runColorpicker(c, st))
}

func runColorpicker(c *tmux.Client, st *colorscheme.Store) error {
	active, err := colorscheme.Active(c)
	if err != nil {
		return err
	}
	m, err := popup.Run(colorpicker.New(colorpicker.Options{
		Backend: colorpicker.TmuxBackend{C: c, Store: st},
		Active:  active,
	}))
	var res colorpicker.Result
	if pm, ok := m.(colorpicker.Model); ok && err == nil {
		res = pm.Result()
	}
	// Anything but ⏎ (including a failed program) reverts to the saved scheme.
	if ferr := colorpicker.Finish(c, st, res); err == nil {
		err = ferr
	}
	return err
}

// listColorschemes prints one row per scheme: "*" for the saved choice, name,
// family, dark/light, bundled/user/override, then "active" (what the server
// shows, when inside tmux) and "invalid: <reason>".
func listColorschemes(e *env, st *colorscheme.Store) error {
	all, err := st.List()
	if err != nil {
		return err
	}
	active := ""
	if c, err := tmux.FromEnv(e.getenv); err == nil { // inside tmux: reconcile, as every run does
		if err := state.Reconcile(c); err != nil {
			return err
		}
		active, _ = colorscheme.Active(c)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\tNAME\tFAMILY\tTONE\tSOURCE\tNOTE")
	for _, s := range all {
		mark, tone := "", s.Tone()
		if s.Saved {
			mark = "*"
		}
		var notes []string
		if s.Name == active {
			notes = append(notes, "active")
		}
		if !s.Valid() {
			tone = "-"
			notes = append(notes, "invalid: "+s.Reason())
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, s.Name, s.Family, tone, s.Source, strings.Join(notes, ", "))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	r, err := st.Resolve()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "\nsaved: %s (* in %s)\n", r.Saved, st.SavedPath())
	if r.Warning != "" {
		fmt.Fprintf(e.stdout, "warning: %s\n", r.Warning)
	}
	return nil
}
