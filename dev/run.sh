#!/usr/bin/env bash
# make dev: build herdmux and start a fresh, isolated `tmux -L herdmux-dev`
# server that sources the real ~/.config/tmux/tmux.conf (read-only) and then
# runs `herdmux init` (dev/tmux.conf), then open it in a Windows Terminal tab (WSL).
#
#   make dev            # server + wt.exe tab
#   NO_WT=1 make dev    # headless: server only (attach with the printed command);
#                       # use this outside WSL
#   tmux -L herdmux-dev kill-server
#
# The server's environment: .dev/bin (the built herdmux) and dev/shims (the
# per-agent wrappers) first on PATH, HERDMUX_CONFIG_DIR=.dev/config, and no
# TMUX, TMUX_PANE, HERDR_* or HERDMUX*. HERDMUX_CONFIG_DIR is always set: it
# wins over XDG_CONFIG_HOME, which is inherited (other tools in dev panes use
# it) and would otherwise point herdmux at the real config dir. Panes run `bash --rcfile dev/bashrc`,
# which puts both dirs back in front after ~/.profile and ~/.bashrc have
# prepended theirs. Every tmux call here passes -L herdmux-dev explicitly;
# herdmux itself has no dev code path.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
dev=$root/.dev
mkdir -p "$dev/bin" "$dev/config"
(cd "$root" && go build -o "$dev/bin/herdmux" ./cmd/herdmux)

T=(tmux -L herdmux-dev)
"${T[@]}" kill-server 2>/dev/null || true

scrub=(-u TMUX -u TMUX_PANE)
for v in $(compgen -e); do
  case $v in HERDR_* | HERDMUX*) scrub+=(-u "$v") ;; esac
done
env "${scrub[@]}" PATH="$dev/bin:$root/dev/shims:$PATH" HERDMUX_CONFIG_DIR="$dev/config" \
  "${T[@]}" -f "$root/dev/tmux.conf" start-server \; \
  set -g default-command "exec bash --rcfile '$root/dev/bashrc'" \; \
  new-session -d -s herdmux -c "$root" -x 200 -y 50

attach=("${T[@]}" attach -t =herdmux)
if [ -n "${NO_WT:-}" ]; then
  echo "herdmux-dev is up. Attach: ${attach[*]}"
  exit 0
fi
wt.exe -w 0 new-tab --title herdmux-dev wsl.exe -d "${WSL_DISTRO_NAME:?not in WSL}" -- "${attach[@]}"
