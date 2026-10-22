package main

import (
	"flag"

	"herdmux/internal/install"
	"herdmux/internal/tmux"
)

// defineInstall is `herdmux install [--mcp] [--dry-run]`.
// The setup steps around it are in docs/install.md.
func defineInstall(fs *flag.FlagSet) func(*env, []string) error {
	withMCP := fs.Bool("mcp", false, "also register the herdmux MCP server with each agent")
	dry := fs.Bool("dry-run", false, "print a diff of every change and write nothing")
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("install takes no arguments")
		}
		return install.Install(install.Options{
			Getenv: e.getenv, Out: e.stdout, MCP: *withMCP, DryRun: *dry,
			Tmux: func() (*tmux.Client, error) { return installTmux(e) },
		})
	}
}

// installTmux is the server whose global PATH install checks: the one in
// $TMUX, else tmux's default server (refused inside test binaries).
func installTmux(e *env) (*tmux.Client, error) {
	if e.getenv("TMUX") != "" {
		return tmux.FromEnv(e.getenv)
	}
	return tmux.Default()
}

// defineUninstall is `herdmux uninstall [--purge]`.
func defineUninstall(fs *flag.FlagSet) func(*env, []string) error {
	purge := fs.Bool("purge", false, "also remove ~/.config/herdmux")
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("uninstall takes no arguments")
		}
		return install.Uninstall(install.Options{Getenv: e.getenv, Out: e.stdout, Purge: *purge})
	}
}
