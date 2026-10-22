// Package colorscheme loads, validates and applies colorschemes.
//
// A scheme is a flat TOML file with one [colors] table holding the 16 slots
// (Slots) as "#rrggbb" strings. 25 schemes are embedded (colors/*.toml); user
// schemes live in <config dir>/colors/<name>.toml (config.Dir), and a user file with
// a bundled name replaces that scheme entirely. The saved choice is the one
// name in <config dir>/colorscheme (default flexoki-dark).
//
// Store is the library surface the CLI, the picker and the MCP tools share:
// List, Get, Raw, Saved, Save, Write, Delete and Resolve. Apply restyles a
// whole tmux server in one call; ApplySaved is init's step and Set is "choose
// this scheme".
package colorscheme
