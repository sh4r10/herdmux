package main

import (
	"flag"
)

// verbs is the CLI surface. The visible names match the [keys] action names
// in config.toml. Hidden verbs have stable names because tmux hooks and
// installed files call them.
var verbs = []verb{
	{name: "init", summary: "turn the layer on (run from tmux.conf: run-shell 'herdmux init')",
		needsTmux: true, define: defineInit},
	{name: "picker", summary: "open the agent picker popup",
		needsTmux: true, define: definePicker},
	{name: "organiser", summary: "open the window organiser popup",
		needsTmux: true, define: defineOrganiser},
	{name: "next-attention", summary: "jump to the next agent that needs attention",
		needsTmux: true, define: defineNextAttention},
	{name: "launch", synopsis: "[agent]", summary: "launch an agent, or open the launch prompt with no argument",
		needsTmux: true, define: defineLaunch},
	{name: "sessionizer", summary: "open the session switcher / project launcher",
		needsTmux: true, define: defineSessionizer},
	{name: "colorscheme", synopsis: "[name | --list]", summary: "open the colorscheme picker, apply and save a scheme, or list schemes",
		define: defineColorscheme}, // needs tmux, except for --list
	{name: "list", synopsis: "[--json]", summary: "list every agent: session, window, name, state, Attention",
		needsTmux: true, define: defineList},
	{name: "mcp", summary: "serve the herdmux MCP server over stdio (needs HERDMUX=1)",
		define: defineMCP}, // file tools work outside tmux; live tools error there
	{name: "report", synopsis: "<working|needs-input|done|exit> --agent <name> [--pid N] [--pane %N]",
		summary:   "report an agent's state for the pane (the integration point; always exits 0)",
		needsTmux: true, neverFail: true, define: defineReport},
	{name: "install", synopsis: "[--mcp] [--dry-run]", summary: "install the tmux.conf block and the agent integrations",
		define: defineInstall},
	{name: "uninstall", synopsis: "[--purge]", summary: "remove exactly what install wrote",
		define: defineUninstall},
	{name: "man", summary: "show the full manual (prints the roff source when not on a terminal)",
		define: defineMan},
	{name: "help", synopsis: "[--all | command]", summary: "show help; --all includes hidden commands"},

	// Hidden.
	{name: "hook", synopsis: "claude|codex", summary: "the claude and codex hook adapter (hook JSON on stdin; always exits 0)",
		hidden: true, neverFail: true, define: defineHook},
	{name: "tmux-hook", synopsis: "<event> [args]", summary: "the entry point for tmux hooks (always exits 0)",
		hidden: true, neverFail: true, define: defineTmuxHook},
}

// help's define reads verbs, so it is set here to avoid an initialization cycle.
func init() { lookup("help").define = defineHelp }

func defineHelp(fs *flag.FlagSet) func(*env, []string) error {
	all := fs.Bool("all", false, "include hidden commands")
	return func(e *env, args []string) error {
		switch len(args) {
		case 0:
			printHelp(e.stdout, *all)
			return nil
		case 1:
			v := lookup(args[0])
			if v == nil {
				return usagef("unknown command %q", args[0])
			}
			sub := flag.NewFlagSet("herdmux "+v.name, flag.ContinueOnError)
			v.define(sub)
			printVerbHelp(e.stdout, v, sub)
			return nil
		default:
			return usagef("too many arguments")
		}
	}
}
