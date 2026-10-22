# claude hook fixtures

Recorded once from real claude (Claude Code 2.1.301, `--model haiku`) on `tmux -L herdmux-dev`, 2026-10-16. Each `<scenario>.jsonl` is one run: every hook's stdin, verbatim, one JSON object per line, in the order claude fired them. The capture hook was hooked on every event (including `PostToolBatch` and `Subagent*`, which herdmux doesn't hook), so the replays also prove those are ignored. `TestHookClaudeReplay` (`cmd/herdmux/hook_cli_test.go`) feeds them into `herdmux hook claude` and checks the state after each line; it never runs claude.

| Scenario | How it was driven (claude args, then keys) | Events |
|---|---|---|
| `prompt` | auto mode; `Reply with just the word OK.`, then `/clear`, then `kill-server` | SessionStart, UserPromptSubmit, Stop, SessionEnd `clear`, SessionStart `clear`, SessionEnd `other` |
| `tool` | `--permission-mode default`; `Use the Read tool to read notes.txt, then tell me its first word.` | … PreToolUse/PostToolUse Read, PostToolBatch, Stop, internal SubagentStop (`agent_id`, `agent_type: ""`) |
| `permission` | `--permission-mode default`; `Use the Bash tool to run exactly: touch made-by-claude.txt`, approve with `1`, then `kill-server` | … PermissionRequest, Notification `permission_prompt` (+6 s), PostToolUse, Stop, SubagentStop, SessionEnd `other` |
| `ask` | `--permission-mode default`; `Use the AskUserQuestion tool to ask me one question: red or blue? Then reply with my answer.`, answer `2` | … PermissionRequest AskUserQuestion, Notification, PostToolUse, Stop, … |
| `subagent` | `--permission-mode default`; `Use the Agent tool to launch one general-purpose subagent that reads notes.txt with the Read tool and reports its contents. Then tell me what it found.` | … SubagentStart, Stop with `background_tasks` (subagent), the subagent's tool events with `agent_id`, SubagentStops, UserPromptSubmit (the result fed back), Stop `[]`, internal SubagentStop |
| `background` | `--permission-mode auto`; `Use the Bash tool with run_in_background set to true to run: sleep 8; echo finished. Do not wait for it; end your turn right away.` | … Stop with `background_tasks` (shell), SubagentStop, UserPromptSubmit, Stop `[]`, … |
| `exit` | `--permission-mode default`; `/exit` | SessionStart, SessionEnd `prompt_input_exit` |

`StopFailure` (an API error) can't be provoked on demand, so it is covered by hand-written events in the tests.

## Re-recording

`record.sh` starts claude in a throwaway project dir on `tmux -L herdmux-dev` (it kills that server first), with `HERDMUX=1`, `HERDR_*` unset and a per-launch `--settings` capture hook that appends to `<scenario>.jsonl` here. `~/.claude/settings.json` is not touched; claude itself records the folder trust and the session as usual.

```bash
bash internal/integrations/claude/testdata/record.sh permission --permission-mode default
T="tmux -L herdmux-dev"
$T send-keys -t rec Down; $T send-keys -t rec Enter             # trust the throwaway folder
$T send-keys -t rec -l 'Use the Bash tool to run exactly: touch made-by-claude.txt'; $T send-keys -t rec Enter
$T capture-pane -p -t rec                                         # wait for the dialog, then:
$T send-keys -t rec 1                                             # approve
$T kill-server                                                    # ends with SessionEnd other
```

After re-recording, update the expected states in `TestHookClaudeReplay` if the event sequence changed.
