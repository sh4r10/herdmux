# herdmux

An AI-agent awareness layer on top of the user's existing tmux setup.

## Language

**Session**:
A tmux session; in practice one per project, created from the project's directory by herdmux's sessionizer (or by hand).
_Avoid_: workspace, project (when meaning the tmux object)

**Agent**:
A pane in which an agent CLI (claude, codex, opencode or pi) is running. Belongs to the **Session** its pane currently sits in; moving the pane moves the agent. There is no registry spanning sessions.
_Avoid_: bot, worker, assistant

**Agent state**:
One of `working`, `needs-input` (blocked on the user: a permission prompt or question), `done` (finished its turn; the user's move) or `unknown` (recognised by its process name only, with no state reporting).

**Attention**:
A sticky flag on an **Agent**, raised whenever it moves into `needs-input` or `done` while its window is not shown in any attached client. Its kind follows the agent's latest `needs-input`/`done` state. Cleared only by a **Visit**. Never times out. An agent that exits or dies takes its Attention with it.

**Visit**:
The agent's window being shown in an attached client. It clears Attention on every agent in that window, whatever their state; the agent's pane does not need to be active.

**Colorscheme**:
A named palette file that styles the whole tmux look: pane backgrounds, status bar, window tabs, borders, popups, menus and messages, as well as everything herdmux draws. Bundled schemes can be overridden by the user's own, and one is chosen by name, the way nvim colorschemes work.
_Avoid_: theme (as a separate concept)
