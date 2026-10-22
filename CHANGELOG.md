# Changelog

## 1.2.0 — 2026-10-22

- Changed: the config directory honours `$XDG_CONFIG_HOME` (`$XDG_CONFIG_HOME/herdmux`); move your config there if you set it.
- New: a sessionizer popup (prefix g by default) to switch tmux sessions or launch a project in a new one.
- New: `herdmux man` shows the full manual (`herdmux man > herdmux.1` saves the page).
- Fixed: the sessionizer shows config.toml errors, and relative or `~` roots are reported instead of being searched from the current directory.
- Fixed: agent names, launch directories and popup error messages containing `#` are now passed to tmux literally instead of being expanded as formats.
- Changed: `herdmux install` no longer tells you to run `t7x install` when herdmux is not on PATH; it asks you to put the binary in a directory on PATH.
- Changed: t7x now warns after install or upgrade if tmux isn't on PATH.
