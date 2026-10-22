#!/usr/bin/env bash
# Records one claude hook fixture: starts real claude on `tmux -L herdmux-dev`
# (killing whatever runs there) with a per-launch --settings capture hook that
# appends each hook's stdin, one JSON object per line, to <scenario>.jsonl
# here. ~/.claude is not touched. A one-off manual step; make check never runs
# it. See README.md for the scenarios and the keys to send.
#
#   bash record.sh <scenario> [claude args...]   e.g. record.sh permission --permission-mode default
#   tmux -L herdmux-dev attach -t =rec            # or drive it with send-keys
#   tmux -L herdmux-dev kill-server               # ends the run (SessionEnd other)
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
sc=${1:?scenario}; shift
work=$(mktemp -d)
mkdir -p "$work/proj"
echo "hello herdmux" > "$work/proj/notes.txt"
out=$here/$sc.jsonl
: > "$out"
cat > "$work/capture.sh" <<EOF
#!/bin/sh
{ cat; echo; } >> "$out"
EOF
chmod +x "$work/capture.sh"
python3 -I - "$work" <<'EOF'
import json, sys
work = sys.argv[1]
evs = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolBatch", "PermissionRequest",
       "Notification", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "SessionEnd"]
hook = [{"hooks": [{"type": "command", "command": work + "/capture.sh"}]}]
json.dump({"hooks": {e: hook for e in evs}}, open(work + "/capture-settings.json", "w"))
EOF
T=(tmux -L herdmux-dev)
"${T[@]}" kill-server 2>/dev/null && sleep 4 || true
scrub=(-u TMUX -u TMUX_PANE)
for v in $(compgen -e); do case $v in HERDR_* | HERDMUX*) scrub+=(-u "$v") ;; esac; done
env "${scrub[@]}" HERDMUX=1 "${T[@]}" -f /dev/null new-session -d -s rec -c "$work/proj" -x 200 -y 50 \
  "claude --model haiku --settings $work/capture-settings.json $*"
echo "recording $sc into $out (claude in $work/proj)"
