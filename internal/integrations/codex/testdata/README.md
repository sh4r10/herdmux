# codex hook fixtures

None recorded yet. `record.sh` records one, the way `internal/integrations/claude/testdata/record.sh` does for claude: real codex on `tmux -L herdmux-dev`, per-launch `-c hooks.<Event>=[…]` capture overrides (`~/.codex` is not touched), every hook's stdin appended verbatim to `<scenario>.jsonl`, one JSON object per line. The launch passes `--dangerously-bypass-hook-trust` because the only hooks are the capture command.

Until then, `TestHookCodex` (`cmd/herdmux/hook_cli_test.go`) drives hand-written events from the codex hooks reference (https://developers.openai.com/codex/hooks): the same protocol as claude's, with `Interrupt` and without `Notification`, `StopFailure` or `background_tasks`.

Scenarios worth recording, and what they should show:

| Scenario | How to drive it | Expect |
|---|---|---|
| `prompt` | `Reply with just the word OK.`, then `/quit` | SessionStart, UserPromptSubmit, Stop, SessionEnd `other` |
| `permission` | `--ask-for-approval untrusted`; `Run exactly: touch made-by-codex.txt`, approve | … PermissionRequest, PostToolUse, Stop |
| `ask` | plan mode; a prompt that makes codex ask a question | PreToolUse `request_user_input`, PostToolUse, … |
| `interrupt` | a long task, then Esc | … Interrupt (is a Stop fired too?) |

After recording, check each line parses (`TestFixturesParse` in the claude package is the model) and replay it in `hook_cli_test.go`, updating `TestHookCodex` if the real sequence differs.
