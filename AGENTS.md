# herdmux

An AI-agent awareness layer on top of an existing tmux setup, written in Go + bubbletea/lipgloss. It tracks the state of claude, opencode and pi agents running in tmux panes, shows it in the status bar, and adds popups to jump between agents. `docs/install.md` covers installing it.

Read `CONTEXT.md` first. It defines the domain vocabulary (Session, Agent, Agent state, Attention, Visit, Colorscheme) and the terms to avoid.

Before changing code, read `docs/build-conventions.md`: module path, CLI dispatch, logging, the tmux wrapper, the `internal/tmuxtest` API and each package's API. `make check` must pass before every commit.

## Hard constraint

Nothing may touch the developer's live tmux server or their global agent config (`~/.claude`, `~/.claude.json`, `~/.config/opencode`, `~/.pi`), their real `~/.config/herdmux` or `~/.config/tmux/tmux.conf`. Running `herdmux install` against the real HOME is the developer's to do, by hand, from `docs/install.md`.

- Do manual tmux work on the isolated dev server, `tmux -L herdmux-dev` (`make dev`; it kills and restarts that server; `NO_WT=1 make dev` runs it headless), or on a private `tmux -L herdmux-test-<name>` socket that you kill afterwards. Tests use `internal/tmuxtest`.
- Pass that `-L` explicitly on every call made from outside the server. Never use `-L default` or `-S`.
- Load agent integrations per launch only (`claude --settings`, `OPENCODE_CONFIG_DIR`, `pi -e`); the shims in `dev/shims/` do this for the dev server.
- herdmux coexists with herdr, a separate agent tool that sets `HERDR_*` env vars. Unset `HERDR_*` in dev launches, and never reuse `HERDR_*` names (ours are `HERDMUX_*` env vars and `@herdmux_*` tmux options).
