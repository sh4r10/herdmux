# herdmux build conventions

Shared conventions for herdmux's code. Change them only on purpose, and update this file when you do.

## Module and layout

- Module path: `github.com/sh4r10/herdmux`. Imports look like `github.com/sh4r10/herdmux/internal/tmux`.
- `go 1.27`, `toolchain go1.27.3`. staticcheck and govulncheck are `tool` directives, run with `go tool`.
- `cmd/herdmux/` holds the thin CLI only. The work goes in `internal/<feature>/`. Every package is built; a `doc.go` that remains holds only the package comment.
- `internal/logfile` is the shared log (see Logging).
- Keep dependencies current: `go get -u` the ones you add, and govulncheck must stay clean.
  - Exception: `go-runewidth` is held below v0.0.26 (and so `x/ansi` at v0.11.8), because v0.0.26+ fills a 2 MB table in its `init`, about 65 ms per process on WSL2, paid twice per popup key (opener and inner run). `TestPackageInitIsCheap` fails if any package init passes 20 ms; lift the hold once upstream makes that table lazy.
- TUI: `github.com/charmbracelet/bubbletea` v1.3.10 and `lipgloss` v1.1.0 (the v1 line, not `charm.land/*/v2`), with `x/ansi` for widths and truncation and `muesli/termenv` for the truecolor profile. Don't mix in v2.

## Gates

`make check` runs gofmt (from the go1.27.3 toolchain), `go vet`, `go tool staticcheck`, `go tool govulncheck`, `go test ./...` (with `TMUX`, `TMUX_PANE` and `HERDR_*` unset) and `node --test` over `internal/integrations/**/*.test.{js,mjs}` (skipped while there are none). `make build` writes `./herdmux` (gitignored). `make check` must pass before every commit.

## CLI dispatch (`cmd/herdmux`)

- `verbs.go` holds the verb table. Each `verb` has `name`, `synopsis`, `summary`, `hidden`, `needsTmux`, `neverFail` and `define`.
- `define(fs *flag.FlagSet) func(e *env, args []string) error` registers the verb's flags and returns its run function. Flags may sit anywhere among the positional args (`report working --agent claude`), and `--` ends flag parsing. `--help` is handled for you.
- To add a verb, add a row to the table with its `define`. Keep the flags of existing verbs stable: key bindings, tmux hooks and installed integrations call them.
- Verbs read the environment only through `e.getenv` (and their parent pid through `e.ppid`) and write only to `e.stdout`/`e.stderr`. That lets `cli_test.go` run the whole CLI in-process and in parallel (see `runCLI`).
- Get a tmux client with `tmux.FromEnv(e.getenv)`.
- Return errors; the dispatcher maps them to exit codes:
  - `nil` → 0
  - `usagef(...)` or a flag error → 2, with the verb's usage
  - `errNotInTmux` → 2 with `herdmux: not inside tmux`. `needsTmux: true` checks `$TMUX` before the run function, after `--help`.
  - anything else → 1, printed as `herdmux <verb>: <err>`
- `neverFail` verbs (`hook`, `report`, `tmux-hook`) always exit 0. Errors, usage errors, "not inside tmux" and panics go to the log, and nothing is printed. `hook` is not `needsTmux`: outside tmux or without `HERDMUX=1` it does nothing at all (no tmux call, no log line), so the globally installed hooks stay silent.
- The version is the `version` const in `cli.go`.

## Logging (`internal/logfile`)

- `logfile.Printf(getenv, format, args...)` appends `<RFC3339> pid=<pid> <msg>` to the log.
- The log is `$XDG_STATE_HOME/herdmux/log` if `XDG_STATE_HOME` is absolute, else `~/.local/state/herdmux/log`.
- Pass the verb's `e.getenv`, never `os.Getenv`, so tests log into their temp HOME.

## tmux wrapper (`internal/tmux`)

- It is the only place that execs `tmux`. Nothing else may call `exec.Command("tmux", ...)`, except `internal/tmuxtest` (which starts servers and attached clients).
- `tmux.FromEnv(getenv)` returns a client for the server in `$TMUX`. The child gets that `TMUX` and no socket flag. It returns `tmux.ErrNoServer` when `TMUX` is unset.
- `tmux.OnSocket(name)` returns a client that passes `-L name`. It rejects `default`, `""` and names containing `/`. `-S` is never passed.
- `tmux.Default()` is tmux's default server (no flag, `TMUX` removed). Its only user is `herdmux install`'s read-only global-PATH check when run outside tmux. Inside a test binary it returns `tmux.ErrDefaultInTest`.
- Fail-closed: inside a test binary (`testing.Testing()`), both constructors reject any server whose name doesn't start with `herdmux-test-`. A test that inherits the user's `$TMUX` gets an error, not their server.
- Methods: `Run(args...)` (stdout without the trailing newline; the error carries stderr), `Display(target, format)`, `Command(args...)` (an `*exec.Cmd` for streaming or popups) and `Args(args...)`.
- `RunList(args...)` is `Run` for server-wide listings (`list-panes -a`, `list-clients`): tmux 3.5 fails those with `no current target` while the server has no session, which is exactly when tmux.conf's `run-shell 'herdmux init'` runs at server start. It returns an empty listing instead. Use it for any `-a` listing init (or anything at server start) can reach. (`display -p` with no target and `show-options -g` work with no session.)
- `KeyBinding(table, key)` / `KeyNote(table, key)`: the `bind-key` line and the `-N` note of one key, picked out of the whole table's `list-keys` (tmux 3.7 prints nothing for `list-keys -T <table> <key>`). `config.Bind` restores old bindings through them, and tests read keys back with `s.Client.KeyBinding`, never `list-keys -T <table> <key>`.
- Format helpers: `tmux.Fields(vars...)` joins with a tab (`tmux.FieldSep`; tmux escapes `\x1f` as `\037`, so don't use it), `tmux.Split(line)` and `tmux.Lines(out)`.

## Tests (`internal/tmuxtest`)

```go
s := tmuxtest.New(t)            // tmux -L herdmux-test-<hex> -f /dev/null, session "main", 200x50; killed in t.Cleanup
s.Pane, s.Home, s.ConfigDir     // first pane (%0); temp HOME; HERDMUX_CONFIG_DIR
s.Tmux("set", "-g", "focus-events", "on")       // any tmux command; fails the test on error (TmuxErr returns it)
s.Display(s.Pane, "#{window_active_clients}")
s.PaneOption(s.Pane, "@herdmux_state")          // "" if unset
s.ListPanes("#{pane_id} #{@herdmux_agent}")
r := s.Herdmux("report", "working", "--agent", "claude")      // built binary, run as from s.Pane
r = s.Run(tmuxtest.Cmd{Args: ..., Pane: "%1", Stdin: json, Env: []string{"X=1"}, Outside: false})
r.Code, r.Stdout, r.Stderr
s.LogPath()                                     // the herdmux log under s.Home
s.Eventually(2*time.Second, "what", func() bool {...})        // poll for run-shell -b / hook effects
c := s.Attach("main")                           // real client via script(1); c.TTY, c.FocusIn(), c.FocusOut(),
c.Write("\x1b[I"); c.Format("#{client_flags}"); c.Output(); c.Close()   // c.Output() is all bytes the terminal got (count BELs here)
s.Client                                        // a *tmux.Client on the server, for in-process package code
```

- The server and every herdmux run get a scrubbed environment:
  - removed: `TMUX`, `TMUX_PANE`, `HERDR_*`, `HERDMUX*`
  - set: `HOME` and the `XDG_*` dirs in a temp dir, `HERDMUX_CONFIG_DIR`, `SHELL=/bin/sh` (inherited `XDG_CONFIG_HOME`/`HERDMUX_CONFIG_DIR` never reach the server: `TestInheritedConfigEnvIsReplaced`)
  - the built herdmux binary's dir first on `PATH`, so hooks and bindings that call a bare `herdmux` work
- In-pane runs also get `TMUX=<socket>,<pid>,0` and `TMUX_PANE`.
- The binary is built once per test process (`tmuxtest.Binary(t)`) into `$TMPDIR/herdmux-tmuxtest-<uid>-<checkout hash>/herdmux`.
- Use `t.Parallel()` freely; never `t.Setenv` for TMUX-like vars. In-process code under test should take a `getenv` or a `*tmux.Client` (`s.Client`).
- Send bubbletea popups one key per `send-keys`.
- Manual checks use `tmux -L herdmux-dev` only (`make dev`, below).
- A pane started with a command (`new-window "sleep 100000"`) runs it under `sh -c`, so `#{pane_current_command}` is `sh`. To fake a named agent process use `exec bash -c 'exec -a claude sleep 100000'`, or `send-keys` that into an interactive `/bin/sh` pane.

## Agent state (`internal/state`)

It is the **only** code that writes the five pane options. `TestOnlyStateNamesTheFiveOptions` fails if any non-test, non-comment line in `cmd/`, `internal/` or `dev/` spells `@herdmux_agent/state/attention/since/pid`, so build formats from the constants: `"#{" + state.OptState + "}"`.

| Constant | Option | Values |
|---|---|---|
| `state.OptAgent` | `@herdmux_agent` | `claude` / `opencode` / `pi` (`state.Agents`, `state.IsAgent`) |
| `state.OptState` | `@herdmux_state` | `state.Working` / `NeedsInput` / `Done` / `Unknown` |
| `state.OptAttention` | `@herdmux_attention` | empty / `needs-input` / `done` |
| `state.OptSince` | `@herdmux_since` | epoch seconds of the last state change |
| `state.OptPID` | `@herdmux_pid` | the agent pid (for `unknown`: the pane's foreground pgrp) |

`state.AllOpts` lists all five. No window-level options exist.

API (all take the `*tmux.Client` from `tmux.FromEnv(e.getenv)`, or `s.Client` in tests):

- `state.Reconcile(c) error`: **call it first in every verb and hook** that touches tmux (picker, jump, launch, mcp tools, `hook claude`, new `tmux-hook` events). One `list-panes -a`, `/proc` reads, at most one write call. It drops reporting agents whose pid is dead or doesn't own the pane's foreground (see below), and sets/unsets `unknown` agents by `#{pane_current_command}` (`state.MatchCommand`: `claude`, semver → claude, `opencode`, `pi`; `node` is no match). `reconciled(e)` in `cmd/herdmux/state_verbs.go` does `FromEnv` + `Reconcile` for verbs.
- `state.Apply(c, state.Report{Pane, Agent, Event, PID, Now}) (state.Outcome, error)`: the report entry point, used by `herdmux report` and in-process by `hook claude`. `Event` is `working|needs-input|done|exit` (`state.Exit`; `state.IsEvent`). `Pane` and `PID` are required here; the CLI fills them from `$TMUX_PANE` and its parent pid. `Now` zero means `time.Now()`. It doesn't reconcile; call `Reconcile` first. It alone decides Attention. `Outcome{Raised, Attention, Session, Window}` tells the attention code whether this report raised Attention (repeats on a flagged agent count) and in which session.
- `state.Visit(c, windowID) error`: clears Attention on every pane of the window (`@N`). Called by `tmux-hook pane-focus-in <window_id>`.
- `state.List(c) ([]state.Agent, error)`: every agent pane in tmux order. `state.Agent` has JSON tags (`session, window_index, window_name, window, pane, agent, state, attention, since, pid`); `herdmux list --json` prints exactly that, and MCP `list_agents` reuses it (adding its own "caller" mark). It doesn't reconcile.
- `state.Init(c) error`: this package's part of `herdmux init`: `focus-events on`, `set-environment -g HERDMUX 1`, and `set-hook -g pane-focus-in[77]` (`state.FocusHookIndex`) running `run-shell -b "herdmux tmux-hook pane-focus-in #{window_id}"`. The fixed slot leaves the user's hooks alone and makes init idempotent. The `init` verb (`defineInit`) runs it, then `Reconcile`; the status bar, colorscheme and config steps go in `defineInit` between the two.

Rules:

- Registration (no agent, a different agent, or `unknown` → a reported state) never raises Attention and starts with none. Otherwise a change into `needs-input`/`done` raises it while `window_active_clients == 0`. While raised, its kind follows `needs-input`/`done`; `working` keeps it. Only `Visit` clears it; `exit` and drops unset all five.
- "Owns the foreground": the pid is alive and either in the tty's foreground process group (tpgid of `pane_pid`, from `/proc`), or an ancestor of that group's leader (so a shell that reports for itself while running `herdmux report` or another child counts). A backgrounded or suspended (Ctrl-Z) agent doesn't. Integrations should pass the agent's own pid: `--pid <process.pid>` (opencode, pi), or the claude pid found by walking up from the hook.
- `tmux-hook <event> [args]` reconciles first, then switches on the event (`defineTmuxHook`). Add git events and `recheck` as new cases there; unknown events are logged.

## Status bar, colours and icons (`internal/statusbar`)

- **Colours**: every herdmux format reads the 16 global `@herdmux_c_<slot>` options. `colorscheme.Slots` lists the slots, `colorscheme.Option(slot)` is the option name and `colorscheme.Ref(slot)` the `#{…}` reference (`internal/colorscheme/slots.go`). Store lowercase hex: in a format, `#D` and similar are aliases.
- **Colorscheme**: `initStatusbar` first runs `initColorscheme` (see Colorschemes, below), which applies the saved scheme. The formats set their own bg/fg from the slots, so a restyle is only option writes.
- **Icons**: `internal/ui/style/icons.go`: `style.StateIcon(state)`, `style.StateSlot(state)` (the slot colouring it) and `style.Icon*` (working U+F111, needs-input U+F059, done U+F058, unknown U+F128, zoom U+F065). Every icon is wider than its cell, so put a space after it.
- **Formats**: `statusbar.TabFormat(current)`, `StatusLeft()`, `StatusRight(home)`. They are pure, with no `#()`. Styles are separate `#[…]` blocks with no commas, so they work inside `#{?…}` branches. The tab's slots are `bg`/`fg_muted` (inactive), `bg_raised`/`fg`/bold (current), `attention_*` + `fg_on_attention` (filled), `state_*` (icons). The bar's slots are `accent` (session), `fg_muted` (dir, stale `…`), `fg` (branch) and `state_working` (dirty `●`).
- **Git options** (per pane; status-right reads the active pane's): `statusbar.OptGitHead` (`@herdmux_git_head`), `OptGitDirty`, `OptGitStale` (`"1"` or unset). `internal/git`: `Read(dir, timeout)` / `Update(prev, dir, timeout)` / `Merge`, `git.Timeout` (2 s). It runs `git --no-optional-locks -c core.fsmonitor=false status`: panes sit in untrusted repos, and a repo's own `core.fsmonitor` is a command.
- **Hooks**: slot `[78]` (`statusbar.HookIndex`) on `statusbar.GitHooks` (`window-pane-changed`, `session-window-changed`, `client-session-changed`) runs `herdmux tmux-hook git-refresh #{pane_id}`. `tmux-hook recheck` rechecks every session's active pane. Its new events are in `statusbarHook`, the `default:` case of `defineTmuxHook`.
- **Recheck loop**: init sets the global `@herdmux_recheck` to a new generation and starts `run-shell -b 'g=<gen>; while sleep 3 && [ "$(tmux show-options -gqv @herdmux_recheck)" = "$g" ]; do herdmux tmux-hook recheck; done'`. An older loop exits at its next tick, and every loop exits when the server dies. Every recheck also reconciles, so attention can rely on a reconcile at least every 3 s.
- `display -p -t %999` (a missing target) **succeeds** with an empty context. Check the returned `#{pane_id}` before writing.

## Config dir (`internal/config`)

- `config.Dir(getenv)` is `$HERDMUX_CONFIG_DIR`, else `config.XDGConfigHome(getenv)/herdmux`: `$XDG_CONFIG_HOME` if absolute (a relative one is ignored, per the XDG spec), else `~/.config` (an error when none of them is usable). install's tmux.conf and opencode paths use the same `XDGConfigHome`. Every config path goes under it: `config.toml`, `colorscheme`, `colors/`. Pass the verb's `e.getenv`.

## config.toml and [keys] (`internal/config`)

- `config.Load(getenv) (Config, error)` reads `config.toml` (`config.Path`); a missing file is `config.Defaults()` with no error. `config.Parse(text)` is the validator (MCP `write_config`: reject on any error). `config.Write(getenv, text)` parses, then saves atomically only if there is no problem. **Both always return a usable `Config`**, even with an error; the error is a `*config.Error` whose text starts `config.toml: ` (show it as is, in red).
- `Config.Agents` is the menu in file order with every `Key` set (auto-picked from the name when omitted; `q` is reserved). `cfg.Visible(e.getenv("PATH"))` is what a prompt shows (first word of `command`, after `VAR=x` words, on PATH). `cfg.Agent(name)`.
- Fallbacks: bad TOML or a wrong type → all defaults; a broken `[[agent]]` (missing/bad name or command, bad key) → default agents; a duplicate agent key or name → the later entry is dropped; a bad `[keys]` entry (unknown action, bad key name, two actions on one key) → default keys; unknown keys elsewhere → reported only. A file with only `[keys]` keeps the default agents.
- `Config.Sessionizer` is `[sessionizer]`: `roots` (directories to list; none → `DefaultSessionizer()`, sessions only) and `depth` (levels below each root, default 3; a negative depth keeps the default). A root that is not `filepath.IsAbs` (`~` included) is dropped with the problem `[sessionizer] roots: "<root>" is not an absolute path (~ is not expanded)`; the valid roots are kept.
- `Config.Keys` maps every `config.Actions` entry (`picker`, `next-attention`, `organiser`, `launch`, `sessionizer`, `colorscheme`; defaults `prefix a`, `prefix u`, `prefix r`, `prefix o`, `prefix g`, unbound) to a canonical spec: `"prefix a"`, `"M-a"` (root) or `""`. `config.ParseSpec` validates tmux key names.
- `config.Bind(c, cfg.Keys)` applies them, idempotently: init's step (`initKeys`, after the status bar so the error is drawn in the scheme's red) and MCP's live rebind after a write. Each binding is `bind-key -N "herdmux: …" -T <table> <key> run-shell -b "herdmux <action> --client '#{client_name}'"` (`config.BindingCommand`). A moved or unbound key gets back what was bound there before herdmux (tmux's default or tmux.conf's binding, note included), from the runtime bookkeeping options `@herdmux_key_<action>` / `@herdmux_key_<action>_restore` (not configuration).
- **Every verb a key runs takes `--client`** (`clientFlag` in `cmd/herdmux/launch_verbs.go`): picker, organiser, next-attention, launch, sessionizer and `colorscheme` declare it. Errors from such runs go through `showOnClient` (red message + log, exit 1 with no stderr, via the `shownError` exit path in `cli.go`).

## Popups (`internal/ui/popup`, `internal/ui/style`)

- **Self-open**: outside a popup, a popup verb calls `popup.Open(c, client, size, "<verb>", args...)` (`openPopup` in `cmd/herdmux/popup_load.go` for the split popups). It runs `display-popup -c <client> -E -B -w -h -e HERDMUX_POPUP=1 -e HERDMUX_CLIENT=<client> '<this binary>' '<verb>' …` and returns at once (display-popup blocks its caller until the popup closes). Inside, `popup.Inside(e.getenv)` is true and the verb runs its bubbletea model with `popup.Run(model)` (alt screen; returns the final model). **Speed**: each tmux call is a client process (10-30 ms on WSL2), and every call before the first frame delays the popup. So the opener makes one call (`list-clients`, in `popup.Client`; `Open` itself makes none before `display-popup`) and does not reconcile (the inner run does, as its first step), and the inner run does Reconcile and its first-frame reads at once with `parallel` (`cmd/herdmux/popup_load.go`). Sizes live in the verb: `popup.Large` (85% × 75%) or `popup.Cells(w, h)`.
- **Client**: `popup.Client(c, e.getenv, *clientFlag)` resolves the client: `--client` (checked against `list-clients`), else `HERDMUX_CLIENT` (inside a popup), else the most recently active client on `$TMUX_PANE`'s session. Popups get `TMUX` but no `TMUX_PANE`.
- **`display -p -c <client>` expands in the caller's `$TMUX_PANE`, not the client's** (and an unknown `-c` name silently falls back). For a client's session, window or pane, read `list-clients -F` and pick its line, as `launch.ClientTarget` does.
- **Look** (`style`): `pal, _ := style.LoadPalette(c)` (the 16 `@herdmux_c_*` the server shows; flexoki-dark for unset slots), `st := style.New(pal)` (truecolor, whatever `TERM` says). `st.Fg(slot)`, `st.On(fg, bg)`, `st.Text()`, `st.Muted()` (hints), `st.Accent()` (bold accent), `st.Error()` (bold `attention_needs_input`), `st.Selected()` (fg bold on `bg_raised`), `st.Bar(sel)` / `st.Row(sel, content, w)` (the accent `▌` bar), `st.Pad(s, w, bgSlot)`, `style.Wrap`, and `st.Frame(title, w, h, lines)`: the rounded border with the title in accent. Popups open with `-B` and draw their own frame. Text styles paint no background (cells show `popup-style`, which the colorscheme sets to `bg`); set `st.Opaque = true` to paint `bg` everywhere for a popup that restyles live (the colorscheme picker: tmux fixes popup-style at open).
- **Errors**: `popup.ShowError(c, client, msg)` is the one red `display-message` (8 s, `#` escaped, `attention_needs_input` via a format, so no palette read).
- **Tests**: Update/View unit tests with golden files (`testdata/<name>.golden` raw ANSI plus `<name>.txt` stripped; `go test ./internal/ui/<pkg> -update` rewrites them; see `internal/ui/launchprompt`). Smoke runs open the real popup on a `tmuxtest` server with `s.Attach`, then send one key per `s.Tmux("send-keys", "-K", "-c", c.TTY, key)`: `-K` feeds the client's key handling, so keys reach the popup (prefix keys work too: `C-b`, then the key, on a `-f /dev/null` server). Wait for the popup with `s.Eventually` on `c.Output()` containing a word of its view. Assert on `ansi.Strip(c.Output())` for text: tmux 3.7 puts SGR resets between `#[…]` segments (and sends RGB to the `xterm-256color` client where 3.5a sent 256-colour escapes), so raw-byte matches across a style change break.

## Split popups (`internal/ui/split`, `internal/ui/picker`)

The picker is the template for the organiser (09) and the colorscheme picker (10). Reuse these pieces instead of copying the picker:

- **Layout**: `split.Layout{Title, Header, Left, Right, Above, Footer}.Render(st, w, h)` draws the whole frame: `st.Frame` with a 1-cell gutter, the header, a blank line, `BodyHeight(h, len(Above))` rows of `left │ right`, the `Above` lines (e.g. the red `config.toml:` error) and the footer. Lines are cut/padded for you. Size the content with `inner := split.Inner(w)`, `lw, rw := split.Columns(inner)` (list 45%, at most 58 cells) and `split.BodyHeight`. Scroll the list with `split.Offset(cur, h)`. Return `""` from View until the first `tea.WindowSizeMsg` and while quitting.
- **Rows**: `st.Row(selected, content, w)`. A selected row's segments must paint `bg_raised` themselves (`st.On(slot, "bg_raised")`); unselected segments use `st.Fg(slot)` and paint no background. `style.Age(since, now)` is the `42s`/`7m`/`3h` form; inject `now` so goldens are stable.
- **Live preview**: `split.Capture(c, pane, n)`: one `capture-pane -p -e -S -n` call, trailing blank lines dropped, the last `n` kept (starts about one panel-height above the cursor). `split.Preview(lines, w, h)` cuts each line and ends it with an SGR reset, so pane colours don't leak. Capture only the selected pane, on every key and on every `split.Tick` (`split.Every()`, 250 ms; return it from `Init` and from each Tick). Not for an `Opaque` popup: the reset drops the painted bg.
- **Filter**: `split.Filter` (`Start` on `/`, `Key(msg) (used, changed)` while typing: text, space, backspace, ⏎ or Ctrl-Y keeps, Esc clears; Ctrl-N/Ctrl-P are not used, so they fall through to the popup and move while typing), `f.Prompt(st)` for the header (`> text█`), `split.Match(text, fields...)` (case-insensitive substring) or `split.Fuzzy(pattern, text) (pos, score, ok)` (fzf-v1-style subsequence match with a rank) with `split.Highlight(text, pos, base, hl)` to render the matched runes. The picker's Esc clears a kept filter first, then closes.
- **Keys**: every list popup takes `ctrl+n`/`ctrl+p` beside `j`/`k`/arrows and `ctrl+y` beside `enter` (also in the organiser's grab and rename modes). `uitest.Key` knows them. q is text while typing.
- **Model shape**: the model talks to tmux only through a small `Backend` interface (`picker.TmuxBackend` for real, a fake in tests), calls it synchronously in Update (captures are a few ms), and returns what the user chose (`picker.Result{Jump, Launch}`); the verb does the jump or launch after `popup.Run` returns. `launch.Switch(c, client, pane)` is the jump (switch-client + select-window + select-pane; the Visit hook clears Attention).
- **Remembered toggles**: server-global runtime options, unset = default (`@herdmux_picker_scope` = `session`, `@herdmux_picker_sort` = `attention`; `picker.LoadSettings`/`SaveSettings`). They die with the server and never go into tmux.conf or config.toml.
- **Stopping an agent**: `state.Stop(c, pane)` sends SIGTERM to the foreground process group of the pane's tty, waits up to 1.5 s for it to leave, then reconciles (which unsets the agent). It refuses with `state.ErrPaneProcess` when the foreground is the pane's own process (an idle shell, or an agent started as the pane's command), because the pane would close.
- **Tests**: `internal/ui/uitest`: `uitest.Golden(t, *update, name, view)` (testdata `.golden` + `.txt`), `uitest.Key("j"|"enter"|"esc"|"tab"|"up"|"backspace"|" "…)`, `uitest.FgSGR(st, slot)` / `BgSGR` (the SGR as the renderer emits it: lipgloss rounds some channels by one, so don't build it from the hex). Smoke runs (`cmd/herdmux/picker_cli_test.go`): open with `herdmux <verb> --client <tty>`, wait for a word of the header in `c.Output()`, sleep ~150 ms, then one key per `send-keys -K -c`. Fake agents: type `bash -c 'exec -a claude sleep 100000'` into a test pane's shell (reconcile marks it `unknown`); a `report` needs `working` then `needs-input` to raise Attention (registration raises none).

## Window organiser (`internal/ui/organiser`)

- `organiser.Model` over a `Backend` (`Windows`, `Capture`, `Swap`, `Rename`/`Unrename`, `Kill`); `organiser.NewTmuxBackend(c, sessionID)` is the real one (it reads `focus-events`). `Result{Jump}` is a window id; the verb runs `organiser.Select`. `organiser.ClientSession` gives the client's session id and name.
- **Targets**: qualify window targets with the session id (`$1:@4`): a bare `@N` linked into several sessions can resolve to another one.
- **tmux 3.5a `swap-window`**: `-d` makes the slot it swapped into current (and no `-d` doesn't), and the current and last windows are index slots, not windows, so any swap moves them. Re-selecting by id alone disturbs prefix `l`. `TmuxBackend.Swap` is the safe step: `set -g focus-events off ; swap-window -d … ; select-window <last> ; select-window <current> ; set -g focus-events on`. The focus-events toggle stops the momentary selects from firing `pane-focus-in` (herdmux's Visit, which would clear the shown window's Attention). Use the same wrapper for any other code that selects a window only to put things back.
- **`rename-window` expands formats** (like `new-window -n`): pass the name through `tmux.FormatLiteral`. `new-window -n` and `rename-window` set a window-local `automatic-rename off`.

## Sessionizer (`internal/sessionizer`, `internal/ui/sessionizer`, 1.2.0)

- `sessionizer.List(c, roots, depth)`: every tmux session (`Entry{Type: "session", Name, Path: #{session_path}, Session: #{session_id}, Pane: #{pane_id} (the active pane, for the preview), Agents: <agent panes>}`), then each directory under `roots` to `depth` (`Type: "dir"`, `Path` the directory), each group by name.
- `sessionizer.SwitchTo(c, client, entry)`: a session entry switches to its id; a dir entry switches to `=SessionName(path)`, creating it with `NewSession` when `HasSession` (`has-session -t =name`, exact) says it's missing. Always target sessions with `=` so `foo` never matches `foobar`.
- The popup (`herdmux sessionizer`, `prefix g`) only picks; the verb calls `SwitchTo` after it closes, with the client name. It opens typing into the filter (no `/`; `q` is text), ranks rows with `split.Fuzzy` on `Name` and highlights the matched runes; `enter`/`ctrl+y` pick, `ctrl+n`/`ctrl+p`/arrows move, Esc clears the filter then closes. The right column is the entry's full path (`ansi.Hardwrap`), then a session's `Backend.Capture` preview or a dir's `os.ReadDir` listing and the session name `SwitchTo` would use. It re-lists every 8th `split.Tick` (2 s: the dir walk) and re-captures on every Tick. It loads `config.toml` on every open and passes the error as `Options.ConfigErr`, drawn in red above the footer (`Above`), as the picker and launch prompt do.

## Manual (`internal/manpage`)

- `internal/manpage/herdmux.1` is herdmux(1) in man(7) roff (not mdoc), embedded; `manpage.Page()` returns it unchanged. It describes the tool only, with no release metadata: `.TH HERDMUX 1 "" "herdmux" "User Commands"` (no version, no date) and no version or history notes in the prose; `TestPageHasNoReleaseMetadata` and `TestManHasNoReleaseMetadata` fail on a `YYYY-MM-DD` date or the `version` const. `herdmux man` pipes it to `man -l -` when stdout is a terminal and `man` is on `PATH`, else prints the roff.
- Update the page with any verb, flag, `config.toml` or key change. `TestManDocumentsEveryVerb` fails if a verb in the table has no `.TP` / `.B herdmux <verb>` entry under COMMANDS; `TestPageLints` runs `groff -man -ww -z -rCHECKSTYLE=3` (skipped without groff; its only allowed diagnostic is the `.TH missing third argument` note about the empty date), so blank lines inside `.EX` are `\&`.

## Launch (`internal/launch`, `internal/ui/launchprompt`)

- `launch.ClientTarget(c, client)` (the client's session and active pane's dir) or `launch.PaneTarget(c, pane, client)`, then `launch.Launch(c, agent, target) (pane, error)`: `new-window -t <session id>: -n <name> -c <dir>`, `send-keys -l <command>` + `Enter` into its shell (which stays after the agent exits), then `switch-client`/`select-window`/`select-pane` for the client. It adds no environment.
- `launchprompt.Prompt{Agents: cfg.Visible(PATH), Err: cfgErr, Where: launchprompt.ShortDir(dir, home)}` is the reusable part: `p.Pick(keyMsg.String())`, `p.Inline(st)` (one line, `launch: c claude · o opencode · p pi`, keys in accent; for the picker's `n` footer), `p.ErrorLines(st, w)` (the red `config.toml:` line). `launchprompt.New(p, st)` is the standalone popup model; `Model.Chosen()` after `popup.Run`.
- `tmux.Literal(s)` escapes user text for an argv: tmux reads a lone or trailing `;` as a command separator (`"x;"` arrives as `"x"`). Use it for typed commands, key names and option values that came from the user.
- `tmux.FormatLiteral(s)` is `#` doubled, then `Literal`. Use it for any non-constant text in an argument tmux expands as a format: `new-session -s`/`-c`, `new-window -n`/`-c`, `rename-window`, `display-message`. Unescaped, a directory named `x#(cmd)` runs `cmd` (sessionizer, launch). Targets (`-t =name`) are not expanded but still need `Literal`.

## Colorschemes (`internal/colorscheme`)

- **Files**: the 25 bundled schemes are `internal/colorscheme/colors/<name>.toml`, embedded. They're lowercase hex with a `# source:` line. User schemes are `<config dir>/colors/<name>.toml`, and a user file with a bundled name replaces it whole (`Override`). Names match `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` (`ValidName`); other files in `colors/` are skipped. The saved choice is `<config dir>/colorscheme`, one name; missing or empty means `colorscheme.Default` (flexoki-dark).
- **Validation** (`Parse(name, file, data)`): every slot in `Slots` must be a `#rrggbb` string in `[colors]`. Other keys and tables are allowed and ignored, and listed in `Scheme.Ignored`. A slot added later goes in `OptionalSlots` (slot → the v1 slot it defaults to), never in `Slots`. `Scheme.Reason()` is the short form for the picker (`bad border, no attention_done`, `bad TOML`, `no [colors]`). `Scheme.Err()` is the long form naming the file (`<path>: border = "#12345" is not #rrggbb; attention_done missing`). `Colors` holds valid slots only, lowercased.
- **`Scheme`**: `Name`, `Family` (one of `Families`; `user` for user-only schemes, the bundled family for overrides), `Light` (pane_bg luminance > 0.5; `Tone()` is `dark`/`light`), `Source` (`Bundled`/`User`/`Override`), `File` (user path, or `bundled:<name>.toml`), `Colors`, `Problems`, `Ignored`, `Saved`.
- **Store** (`colorscheme.FromEnv(e.getenv)` or `NewStore(dir)`): the API for the picker (10) and the MCP tools (13):
  - `List()`: everything in picker order (by family, bundled order inside one, user-only schemes last by name), invalid ones included, with `Saved` marked.
  - `Get(name)` (the effective scheme, maybe invalid; unknown names wrap `ErrNotFound`) and `Raw(name)` (its TOML text: `get_colorscheme`).
  - `Saved()` and `Save(name)` (refuses unknown or invalid schemes and writes nothing).
  - `Write(name, text)`: validates, then writes the user file atomically; a bundled name makes an override. Invalid text is refused with `Err()`, and nothing is written.
  - `Delete(name)`: user files only. Deleting an override brings the bundled scheme back; a bundled name or an unknown name is an error.
  - `Resolve()`: the effective scheme for the saved choice. An unknown or invalid choice falls back to flexoki-dark (the user's override of it if valid, else the embedded one), with a `Warning` naming the file and the slot. The saved file is never changed.
  - `BundledScheme(name)` and `DefaultScheme()` ignore user files.
- **Apply** (`*tmux.Client`):
  - `Apply(c, s)`: one tmux call sets the 16 `@herdmux_c_<slot>`, `@herdmux_colorscheme` (`OptActive`, runtime-only) and every `StyleOptions(s)` option, all `set -g`. Use it for the picker's live preview.
  - `Active(c)`: the scheme the server shows.
  - `Set(c, st, name)`: "choose": refuses unknown or invalid schemes, applies, then saves. It's `colorscheme <name>`, the picker's Enter and MCP `set_colorscheme`.
  - `ApplySaved(c, st)`: init's step, and the picker's Esc revert. It resolves, applies and shows `Warning` with `Message`.
  - After `Write` or `Delete` of the active scheme (`Active(c)`), call `ApplySaved`.
- **CLI**: `colorscheme` is not `needsTmux`. `--list` works anywhere (the `active` note only appears inside tmux). `colorscheme <name>` and the bare picker form return `errNotInTmux` outside tmux. The bare form opens the colorscheme picker popup (below).
- **Init order**: `initColorscheme` → status formats → `initKeys` (06) → `state.Init` → `Reconcile`. Its fallback warning is logged as well as shown, because at server start (tmux.conf) no client is attached yet and the message is lost.

## Colorscheme picker (`internal/ui/colorpicker`)

- `herdmux colorscheme [--client]` with no name: `openColorpicker` in `cmd/herdmux/colorscheme_verbs.go` (self-open, `popup.Large`). Inside, `colorpicker.New(Options{Backend: TmuxBackend{C, Store}, Active})`, then `colorpicker.Finish(c, store, m.Result())`: `Set` for a `Result.Save`, otherwise `ApplySaved` (Esc, q, Ctrl-C, or a failed program all revert).
- **Opaque**: the view builds `style.New(<highlighted scheme's palette>)` with `Opaque = true` on every frame, so the frame, gutter, padding and swatches all carry the highlighted scheme. `split.Layout` now paints its gutter with `st.Style()` (a no-op when not Opaque). No `split.Preview` (its reset drops the painted bg).
- **Saved elsewhere**: every `split.Tick` reads `Store.Saved()` (a file read, no tmux call). On a change it re-lists, clears a filter that hides the new choice, jumps to it, applies it (a no-op restyle after MCP's own apply) and says `saved elsewhere (MCP): <name>` in the footer.

## Attention across sessions (`internal/attention`)

- **next-attention**: `attention.Order(agents)` (needs-input before done, oldest `since` first, ties in tmux order), `attention.Next(c)` (List + Order; reconcile first), `attention.Jump(c, client, pane)` (`switch-client -c` + `select-window` + `select-pane`). The picker (08) or MCP (13) can reuse `Order` for "most urgent" and `Jump` for a cross-session jump.
- **Bell**: every verb that calls `state.Apply` passes the `Outcome`s to `ring(e, c, outcomes...)` (`cmd/herdmux/attention_verbs.go`), once per run, after the writes: `report` and `hook claude` do (`claude.Handle` now returns the final `state.Outcome`). An MCP tool that reports state should do the same. `attention.Bell` batches: `Add(o)` keeps raises only, `Ring(c)` writes one `\a` to the picked client's `#{client_tty}` and one `display-message -c` (`attention.Message`: `<icon> <session>` per session, icon in `attention_*`, name in the message style). Errors are logged, never returned.
- **Picking**: `attention.Clients(c)` / `attention.Pick(cs)`: the focused client, else the highest `client_activity`; several focused clients (tmux marks a client focused on attach) are told apart by activity. The looked-at session is the picked client's session only if it is focused.
- **Tests**: the BELs land in `c.Output()` of `s.Attach` clients only (the wrapper reaches only `herdmux-test-*` servers). `client_activity` is in whole seconds: sleep >1 s before the input that should make a client the most recent. `sleeper(t, s)` (`new-window -d` with no target) lands in the *most recently created* session; use `sleeperIn(t, s, session)` in multi-session tests.

## Dev harness (`make dev`)

- `make dev` runs `dev/run.sh`: builds `.dev/bin/herdmux`, kills and restarts `tmux -L herdmux-dev` with `-f dev/tmux.conf` (sources `~/.config/tmux/tmux.conf` read-only, then `run-shell 'herdmux init'`), and opens a Windows Terminal (`wt.exe`) tab attached to session `herdmux` (WSL only). `NO_WT=1 make dev` starts the server headless and prints the attach command; use it anywhere else.
- The server environment: `.dev/bin` then `dev/shims` first on `PATH`, `HERDMUX_CONFIG_DIR=.dev/config` (gitignored `/.dev/`; it must stay set, since it is what keeps an inherited `XDG_CONFIG_HOME` from pointing herdmux at the real config), and no `TMUX`, `TMUX_PANE`, `HERDR_*` or `HERDMUX*`. Panes get `HERDMUX=1` from init.
- Shims: executable `claude`/`opencode`/`pi` wrappers live in `dev/shims/` (committed). Each must find the real binary further down `PATH` (skip `dev/shims` itself) and `exec` it with the per-launch flags.
- Panes run `bash --rcfile dev/bashrc` (`default-command`, set by `dev/run.sh` before the first session). It sources `~/.profile` (which usually sources `~/.bashrc`), then puts `.dev/bin` and `dev/shims` back in front of `PATH`: startup files commonly prepend dirs such as `~/.local/bin` or `~/.opencode/bin` that hold the real `claude`, `pi` and `opencode`, which would shadow the shims (typed `opencode`/`claude` would run the real binary). Sourcing `~/.profile` matches what tmux's default login shell would read, so the developer's prompt and aliases still load. Panes started with an explicit command (`new-window 'opencode'`) don't run that rc; their `PATH` is the server's, which already has the dev dirs first. Check with `type -a claude pi opencode` in a dev pane.
- Driving it from a script: create windows from inside the server (`tmux -L herdmux-dev run-shell 'tmux new-window -d -n x'`). A `new-window` sent by an outside, unattached command client gives the pane *that client's* `PATH`, which lacks `.dev/bin`. For an attached client, `script -qfec "stty cols 200 rows 50; TERM=xterm-256color exec tmux -L herdmux-dev attach -t =herdmux" /dev/null` with stdin from `sleep N |` works.

## pi extension (`internal/integrations/pi`)

- Source: `internal/integrations/pi/herdmux-agent-state.ts`, embedded as `integrations.PiExtension` (`pi.go`); install name `integrations.PiExtensionName` (`herdmux-agent-state.ts`, for `~/.pi/agent/extensions/`). Header comments `// HERDMUX_INTEGRATION_ID: herdmux-pi-agent-state` and `// HERDMUX_INTEGRATION_VERSION: <n>` (an integer): bump it on every change to the file.
- It is plain JavaScript syntax in a `.ts` file: pi loads it through jiti, and its `node --test` suite (`herdmux-agent-state.test.mjs`) copies it to a temp `.mjs`, so the system node 20 runs it. Don't add type annotations.
- It reports `herdmux report <state> --agent pi --pid <process.pid> --pane <TMUX_PANE>`, on transitions only, one child at a time in order (detached, never awaited, failures ignored). `session_start` registers with `done`.
- Dev: `dev/shims/pi` execs the real pi with `-e <that file>` and `-e dev/pi/herdmux-mcp.ts` (the dev-only `registerMcpServer`, `exposure: "direct"`); the installed extension itself never registers MCP.

## opencode plugin (`internal/integrations/opencode`)

- Source: `internal/integrations/opencode/herdmux-agent-state.js`, embedded as `integrations.OpencodePlugin` (`[]byte`), file name `integrations.OpencodePluginName`. Install writes exactly these bytes to `~/.config/opencode/plugins/herdmux-agent-state.js` (temp HOME in tests).
- Header comments: `// HERDMUX_INTEGRATION_ID=opencode` and `// HERDMUX_INTEGRATION_VERSION=<integer>`, one per line near the top. **Bump the version on every change to the file**; `install --dry-run` compares it. `TestOpencodePluginEmbedded` checks both.
- Only a default export (`{ id, server }`): opencode treats exported functions as plugins, and a module exports either `server()` or `tui()`.
- Tests: `herdmux-agent-state.test.mjs` next to it, run by `make check`'s `node --test` (node 20 loads the `.js` as ESM by syntax detection; no `package.json`).
- Dev shim `dev/shims/opencode`: copies the source into `.dev/opencode/plugins/` on every launch, sets `OPENCODE_CONFIG_DIR=.dev/opencode`, unsets `HERDR_*`, execs the real opencode. It also copies `dev/opencode/opencode.json` (`mcp.herdmux`) to `.dev/opencode/opencode.json` on every launch (opencode adds `$schema` to it and writes `node_modules/`, `package.json` there).

## claude integration (`internal/integrations/claude`)

`herdmux hook claude` (`cmd/herdmux/hook_verb.go`) is the hook adapter.

- `claude.Settings` is the embedded `settings.json`: one entry per event (SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest, Notification with matcher `permission_prompt|elicitation_dialog|elicitation_url_dialog`, Stop, StopFailure, SessionEnd), each `{"type":"command","command":"herdmux hook claude"}` (`claude.Command`), synchronous. It is the one source of the hooks, and stays hooks-only: install merges its `hooks` into `~/.claude/settings.json`; uninstall removes the entries whose command is `claude.Command`. `dev/shims/claude` passes `dev/claude/settings.json` per launch (`--settings`): the same hooks plus the MCP read pre-allows (`TestDevClaudeSettings` in `internal/mcp` fails if its hooks drift from `settings.json`, so copy changes over).
- `claude.Parse` / `claude.Decide(ev) Action` / `claude.Handle(c, pane, pid, ev)` is the mapping; `claude.FindClaude(ppid)` walks up `/proc` to the process whose argv[0] basename or comm matches claude (`state.MatchCommand`). If none is found the hook logs and writes nothing.
- The gate: without `HERDMUX=1` and `TMUX_PANE` the hook drains stdin and returns silently (no tmux call, no log line). Otherwise: parse (malformed → logged), reconcile, decide, find the pid, `state.Apply`.
- Fixtures: `internal/integrations/claude/testdata/<scenario>.jsonl` (one hook stdin per line) with `record.sh` and a README. Tests that walk to "a claude process" must set it explicitly (in-process `env.ppid`, or a bash started as `exec -a claude`): `go test` run from a Claude Code session has the real claude among its ancestors.

## MCP server (`internal/mcp`)

- `herdmux mcp` (`cmd/herdmux/mcp_verb.go`) calls `mcp.Serve(ctx, getenv, stdin, stdout)`: go-sdk v1.8.0 (imported as `sdk`), `StdioTransport` on the real stdio, `IOTransport` otherwise. Not `needsTmux`. Without `HERDMUX=1` it returns `mcp.ErrNotEnabled` (exit 1, `herdmux mcp: HERDMUX=1 is not set: …`). `mcp.Server(getenv)` builds the server for in-process tests (`sdk.NewInMemoryTransports`; see `internal/mcp/server_test.go`, whose `env(s, pane)` is a getenv for a tmuxtest server).
- Names: `mcp.Name` (`herdmux`), `mcp.Command` (`herdmux mcp`), the tool consts and `mcp.Tools`, `mcp.ReadOnly` (the four read tools), `mcp.ClaudeAllow()` (`mcp__herdmux__<tool>` for those four: claude's `permissions.allow`; it ignores `readOnlyHint`).
- Tools return text (JSON for the two lists: `[]mcp.SchemeInfo`, `[]mcp.AgentInfo` = `state.Agent` + `caller`). Failures are tool errors (`IsError`), never prompts. Live tools (`set_colorscheme`, `list_agents`) return `mcp.ErrNoTmux` without `$TMUX`; the file tools work and say what wasn't applied. Every tmux-touching call reconciles first. `write_config` = `config.Write` then `config.Bind`. `write_colorscheme`/`delete_colorscheme` call `ApplySaved` when the name is the active (`Active(c)`) or saved scheme.
- Dev registration (committed, read by the shims; `TestDevRegistrations` checks the shapes): `dev/claude/mcp.json` (`--mcp-config`, placed before `--settings` because it takes several values), `dev/claude/settings.json`, `dev/opencode/opencode.json`, `dev/pi/herdmux-mcp.ts`. The installed integrations never register MCP (also tested).
- **For install `--mcp`**, opt-in, temp HOME in tests: claude `claude mcp add -s user herdmux -- herdmux mcp` (or the `mcpServers.herdmux` entry of `dev/claude/mcp.json` in `~/.claude.json`) and merge `mcp.ClaudeAllow()` into `~/.claude/settings.json` `permissions.allow`; opencode the `mcp.herdmux` entry of `dev/opencode/opencode.json` into `~/.config/opencode/opencode.json`; pi `{"mcpServers":{"herdmux":{"command":"herdmux","args":["mcp"],"env":{},"exposure":"direct"}}}` in `~/.pi/agent/mcp.json` (or `pi mcp add herdmux --exposure direct -- herdmux mcp`). Uninstall removes exactly those entries.

## install / uninstall (`internal/install`)

- `install.Install(Options)` / `install.Uninstall(Options)`; `cmd/herdmux/install_verbs.go` is the thin verb. `Options{Getenv, Out, MCP, DryRun, Purge, Tmux}`. `Tmux` returns the server whose global PATH the preflight checks (the CLI: `FromEnv` inside tmux, else `tmux.Default()`); a "no server running" / "error connecting" error means nothing to check. Uninstall never connects to tmux.
- **Paths** come only from the passed-in env (`install.Resolve(getenv)`): `config.XDGConfigHome` (`$XDG_CONFIG_HOME` if absolute, else `~/.config`) for `tmux/tmux.conf` and `opencode/…`, `$HOME` for `.claude/settings.json`, `.claude.json` (read only), `.pi/agent/extensions/…` and `.pi/agent/mcp.json` (read only), and `config.Dir` for `--purge`.
- **Test guard**: inside a test binary, `Resolve`, every write/remove and every agent-CLI exec refuse any path under the real `$HOME` (not under `os.TempDir()`), and only executables under the temp dir run. Tests use a temp HOME, fake `herdmux`/`claude`/`pi` scripts on a temp PATH, and a `tmuxtest` server or a dead `herdmux-test-*` socket for `Tmux` (`internal/install/install_test.go`).
- Child processes (`sh -c 'command -v herdmux'`, `claude`/`pi mcp …`) get the process env with `HOME`, `PATH`, `XDG_*`, `CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, `HERDMUX_CONFIG_DIR`, `TMUX*` replaced from `getenv`.
- **JSON**: `ojson.go` is an order-preserving tree (`*object` member lists, `json.Number` literals) re-encoded in `JSON.stringify(v, null, indent)` style with the file's own indent unit and final newline, so a file in that style round-trips byte for byte. An edit that changes nothing writes nothing.
- **Ownership markers**: tmux.conf lines between `# >>> herdmux >>>` and `# <<< herdmux <<<` (`install.Block`); claude hooks whose command is `claude.Command` (stripped hook by hook, emptied groups/events/`hooks` dropped); `mcp.ClaudeAllow()` strings; opencode `mcp.herdmux`; plugin files whose `HERDMUX_INTEGRATION_ID` matches the embedded file's (`install.Header`). A file at a plugin path with another id makes install refuse and uninstall leave it.
- Writes are temp file + rename in the target's (symlink-resolved) directory, keeping the mode. `<file>.herdmux.bak` is saved before the first edit of an existing file and never overwritten. uninstall deletes a file only when the result is empty and there is no backup (install created it).
