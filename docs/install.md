# Installing herdmux

These steps put herdmux on your real tmux and agents. Do them in order.

herdmux is built and tested against tmux 3.5a. Building it needs Go 1.27 (`go.mod` pulls the exact toolchain). It reports state for claude, codex, opencode and pi.

## 1. Build the binary and put it on PATH

```bash
go install github.com/sh4r10/herdmux/cmd/herdmux@latest   # lands in $(go env GOBIN) or ~/go/bin
```

Or from a checkout:

```bash
git clone https://github.com/sh4r10/herdmux && cd herdmux
go build -o ~/.local/bin/herdmux ./cmd/herdmux
```

Any directory works, as long as `sh` can find it: tmux's `run-shell` and key bindings run commands through `sh`, not your interactive shell. The directory must be on PATH as an absolute path. A PATH entry written as the literal string `~/go/bin` works in bash but not in `sh`, which doesn't expand it. Check it with:

```bash
sh -c 'command -v herdmux'
```

## 2. Tidy `~/.config/tmux/tmux.conf`

herdmux styles the whole of tmux (status bar, window tabs, borders, popups) from its colorscheme. Remove any tmux theme plugin that does the same, such as `janoamaral/tokyo-night-tmux`; install warns while that one is still loaded. Keep everything else.

## 3. Run `herdmux install`

```bash
herdmux install --dry-run    # shows a diff of every change and writes nothing
herdmux install              # add --mcp to register the MCP server too
```

Before it writes anything, install checks four things:

- `herdmux` is on `sh`'s PATH.
- If a tmux server is running, `herdmux` is also on that server's global PATH.
- Every JSON file it edits parses.
- Every target directory is writable.

If any check fails, install lists every problem and writes nothing. If a write fails partway through, it says which steps finished. Fix the problem and run install again: it's idempotent.

install writes:

| File | Change |
|---|---|
| `~/.config/tmux/tmux.conf` (`$XDG_CONFIG_HOME/tmux/tmux.conf`) | a marked block at the very end, after any TPM line: `# >>> herdmux >>>` / `run-shell 'herdmux init'` / `# <<< herdmux <<<` |
| `~/.claude/settings.json` | claude hook entries with the command `herdmux hook claude`, added next to your own hooks |
| `~/.codex/hooks.json` | codex hook entries with the command `herdmux hook codex`, added next to your own hooks. codex runs new hooks only after you trust them: open codex once and run `/hooks`. |
| `~/.codex/config.toml` | `features.daemon_auto_start = false`, written by `codex features disable daemon_auto_start` (only if the file exists, so codex must be on PATH then). codex's shared background server runs hooks with the environment of whichever launch started it, so herdmux would see a stale `TMUX_PANE`. If a server is running, the next codex launch asks to restart it: choose restart. |
| `~/.config/opencode/plugins/herdmux-agent-state.js` | the opencode plugin. Other plugins and `tui.jsonc` are left alone. |
| `~/.pi/agent/extensions/herdmux-agent-state.ts` | the pi extension. It does nothing inside pi-subagents children. |

The hooks, the plugin and the extension stay off unless `HERDMUX=1` is set, and only `herdmux init` sets it. Before install first edits an existing file, it saves the original as `<file>.herdmux.bak`, and it never overwrites that backup. JSON edits keep your key order and touch only herdmux's entries. To upgrade, rebuild the binary and run install again: it rewrites the plugin files, and `--dry-run` shows any that are stale (by `HERDMUX_INTEGRATION_VERSION`).

### Optional: the MCP server (`--mcp`)

`herdmux install --mcp` also registers `herdmux mcp` with each agent. Run by hand, that's:

| Agent | Command or entry |
|---|---|
| claude | `claude mcp add -s user herdmux -- herdmux mcp`, plus `permissions.allow` for the four read tools (`mcp__herdmux__list_colorschemes`, `list_agents`, `get_colorscheme`, `read_config`) in `~/.claude/settings.json`, so reading never prompts |
| codex | `codex mcp add herdmux -- herdmux mcp` (writes `[mcp_servers.herdmux]` to `~/.codex/config.toml`) |
| opencode | `"mcp": {"herdmux": {"type": "local", "command": ["herdmux", "mcp"], "environment": {}, "enabled": true}}` in `~/.config/opencode/opencode.json` |
| pi | `pi mcp add herdmux --exposure direct -- herdmux mcp` (writes `~/.pi/agent/mcp.json`; `direct` exposes the tools to the model as ordinary tools) |

`--mcp` needs `claude`, `codex` and `pi` on PATH.

## 4. Reload tmux

install never touches the running server. Apply the change with:

```bash
tmux source-file ~/.config/tmux/tmux.conf
```

Or restart the server. Agents that are already running don't have `HERDMUX=1`, so they show as `unknown` until you relaunch them.

## 5. Keys and the sessionizer

After the reload, herdmux binds its keys: `prefix a` agent picker, `prefix u` next agent that needs attention, `prefix r` window organiser, `prefix o` launch an agent, and `prefix g` the sessionizer (session switcher / project launcher). The colorscheme picker has no default key; give it one in `[keys]` (e.g. `colorscheme = "prefix C"`). Change any of them in `[keys]` in `~/.config/herdmux/config.toml` (`$XDG_CONFIG_HOME/herdmux/config.toml` if you set `XDG_CONFIG_HOME`, or `$HERDMUX_CONFIG_DIR/config.toml`), e.g. `sessionizer = "prefix f"` or `""` to unbind.

The sessionizer lists every open session, then the directories under `[sessionizer]` roots. With no roots it lists only sessions:

```toml
[sessionizer]
roots = ["/home/you/projects"]   # absolute paths; ~ is not expanded
depth = 3                        # levels below each root (default 3)
```

A root that isn't an absolute path (`"projects"`, `"~/projects"`) is left out, and the sessionizer shows it in red: `config.toml: [sessionizer] roots: "projects" is not an absolute path (~ is not expanded)`. The other roots are still listed.

⏎ on a session switches to it; ⏎ on a directory switches to the session named after it (everything before its first letter dropped, then each `.` or `:` turned into `_` as tmux does: `12.34-herdmux` → `herdmux`, `12.35-my.proj` → `my_proj`), creating it there first if needed.

## Undo

```bash
herdmux uninstall            # add --purge to remove the config dir too
```

uninstall removes exactly what install added:

- the tmux.conf block
- the hook entries whose command is `herdmux hook claude`, and the four MCP pre-allows
- the hook entries whose command is `herdmux hook codex`, and `codex features enable daemon_auto_start` if the server is off
- both plugin files
- the opencode `mcp.herdmux` entry
- `claude mcp remove -s user herdmux`, `codex mcp remove herdmux` and `pi mcp remove herdmux`, if those servers are registered

It keeps the config dir, `~/.config/herdmux` or `$XDG_CONFIG_HOME/herdmux` (config.toml, the saved colorscheme, your schemes) unless you pass `--purge`, and it leaves the `.herdmux.bak` backups in place. A running server keeps `HERDMUX=1` and the layer (its keys, hooks and styles) until you restart it with `tmux kill-server`: a reload (`tmux source-file`) doesn't unset anything `herdmux init` set, and there is no live teardown verb. Undoing step 2 is up to you.
