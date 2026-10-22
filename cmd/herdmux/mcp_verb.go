package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"herdmux/internal/mcp"
)

// defineMCP is `herdmux mcp`: the stdio MCP server. It
// refuses to start without HERDMUX=1 (exit 1, with the reason on stderr).
func defineMCP(_ *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("mcp takes no arguments")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return mcp.Serve(ctx, e.getenv, e.stdin, e.stdout)
	}
}
