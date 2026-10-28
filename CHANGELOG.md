# Changelog

## 1.5.2 — 2026-10-28

- Fixed: install's codex advice said to restart the background server; codex keeps using a running one even with `daemon_auto_start` off. The advice (and the hook's log line) now say `codex app-server daemon stop`.
- Fixed: an npm-installed codex showed as `node` with no state until its first prompt (codex fires SessionStart only then). Reconcile now recognises a `codex` child under the node wrapper and marks the pane an unknown codex.

## 1.5.1 — 2026-10-28

- Fixed: codex state never reached the status bar. codex's shared background server (`daemon_auto_start`, on by default) runs the hooks with the environment of whichever launch started it, so herdmux reported to a stale pane. `herdmux install` now runs `codex features disable daemon_auto_start` (uninstall re-enables it), the dev shim passes the same override, and a hook run by the server logs what to do instead of "pane not found".

## 1.4.0 — 2026-10-24

- New: the status bar's right side shows Attention in other sessions: the most urgent agent's state icon and its session name, plus `+N` for further waiting agents elsewhere. It updates on every report, Visit and recheck.
- New: a mouse click on the right side of the status bar runs `next-attention` (`MouseDown1StatusRight`, set by `herdmux init`).

## 1.3.0 — 2026-10-23

- Changed: the sessionizer opens typing into its filter, which now fuzzy-matches names (matched letters highlighted, best matches first). Esc clears the filter, then closes; `q` is text.
- New: the sessionizer's right column shows the selected entry's full path and a live preview of a session's active pane, or the session a directory would create or switch to and its contents.
- New: in every popup, Ctrl-N/Ctrl-P move like j/k (also while typing a filter) and Ctrl-Y acts as Enter.
- Fixed: on tmux 3.7, moving or unbinding a herdmux key never restored the binding that was there before (tmux 3.7 prints nothing for `list-keys -T <table> <key>`).
- Changed: the sessionizer re-walks the configured roots every 2 s instead of every 250 ms while open.

## 1.2.0 — 2026-10-22

- Changed: the config directory honours `$XDG_CONFIG_HOME` (`$XDG_CONFIG_HOME/herdmux`); move your config there if you set it.
- New: a sessionizer popup (prefix g by default) to switch tmux sessions or launch a project in a new one.
- New: `herdmux man` shows the full manual (`herdmux man > herdmux.1` saves the page).
- Fixed: the sessionizer shows config.toml errors, and relative or `~` roots are reported instead of being searched from the current directory.
- Fixed: agent names, launch directories and popup error messages containing `#` are now passed to tmux literally instead of being expanded as formats.
- Changed: `herdmux install` no longer tells you to run `t7x install` when herdmux is not on PATH; it asks you to put the binary in a directory on PATH.
- Changed: t7x now warns after install or upgrade if tmux isn't on PATH.
