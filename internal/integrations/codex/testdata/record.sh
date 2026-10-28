#!/usr/bin/env bash
# Records one codex hook fixture: starts real codex on `tmux -L herdmux-dev`
# (killing whatever runs there) with per-launch -c hook overrides whose
# capture command appends each hook's stdin, one JSON object per line, to
# <scenario>.jsonl here. ~/.codex is not touched (overrides only; the launch
# bypasses hook trust because the only hooks are this capture). A one-off
# manual step; make check never runs it. Nothing is recorded yet: when a
# fixture lands, replay it in cmd/herdmux/hook_cli_test.go as the claude
# fixtures are, and compare it with TestHookCodex's hand-written events.
#
#   bash record.sh <scenario> [codex args...]   e.g. record.sh permission --ask-for-approval untrusted
#   tmux -L herdmux-dev attach -t =rec            # or drive it with send-keys
#   tmux -L herdmux-dev kill-server               # ends the run (SessionEnd other)
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
sc=${1:?scenario}; shift
work=$(mktemp -d)
mkdir -p "$work/proj"
echo "hello herdmux" > "$work/proj/notes.txt"
git -C "$work/proj" init -q
out=$here/$sc.jsonl
: > "$out"
cat > "$work/capture.sh" <<EOS
#!/bin/sh
{ cat; echo; } >> "$out"
EOS
chmod +x "$work/capture.sh"
args=(--dangerously-bypass-hook-trust)
hook="[{hooks=[{type=\"command\",command=\"$work/capture.sh\"}]}]"
for ev in SessionStart UserPromptSubmit PreToolUse PostToolUse PermissionRequest Stop Interrupt SessionEnd \
  SubagentStart SubagentStop PreCompact PostCompact; do
  args+=(-c "hooks.$ev=$hook")
done
T=(tmux -L herdmux-dev)
"${T[@]}" kill-server 2>/dev/null && sleep 4 || true
scrub=(-u TMUX -u TMUX_PANE)
for v in $(compgen -e); do case $v in HERDR_* | HERDMUX*) scrub+=(-u "$v") ;; esac; done
env "${scrub[@]}" HERDMUX=1 "${T[@]}" -f /dev/null new-session -d -s rec -c "$work/proj" -x 200 -y 50 \
  "codex ${args[*]} $*"
echo "recording $sc into $out (codex in $work/proj)"
