// HERDMUX_INTEGRATION_ID: herdmux-pi-agent-state
// HERDMUX_INTEGRATION_VERSION: 1
//
// herdmux agent-state extension for pi. Bump HERDMUX_INTEGRATION_VERSION
// whenever this file changes (herdmux install compares it).
//
// It maps pi's lifecycle events to herdmux agent states and runs
// `herdmux report <state> --agent pi --pid <process.pid> --pane <TMUX_PANE>`
// on transitions only:
//
//   session_start           register the pane: done (never raises Attention)
//   agent_start (first)     working; later agent_start/agent_end pairs of the run are ignored
//   ui_prompt_start         needs-input (pi wraps every ctx.ui prompt, outermost only)
//   ui_prompt_end           back to working while a run is active, else done
//   agent_settled           done (once per run, also after abort/error; agent_end is not used)
//   session_shutdown quit   exit (other reasons are followed by a new session_start)
//
// kill -9 and crashes fire nothing; herdmux's reconcile drops the agent then.
// It does nothing for pi-subagents children (PI_SUBAGENT_CHILD=1), outside the
// TUI, without HERDMUX=1 or outside tmux. It never registers an MCP server.
//
// Plain JavaScript syntax on purpose: pi loads it as .ts (jiti), and the
// node --test suite imports the same source under node 20.

import { spawn } from "node:child_process";

export default function herdmuxAgentState(pi) {
  let pane = ""; // TMUX_PANE, read at session_start
  let running = false; // between the first agent_start and agent_settled
  let prompting = false; // between ui_prompt_start and ui_prompt_end
  let reported = ""; // the last state sent, so only transitions are reported
  let exited = false;

  // Reports run one at a time, in order, never awaited by pi's handlers.
  const queue = [];
  let busy = false;

  function active(ctx) {
    const env = process.env;
    if (env.PI_SUBAGENT_CHILD === "1") return false;
    if (env.HERDMUX !== "1" || !env.TMUX_PANE) return false;
    return !!ctx && ctx.mode === "tui";
  }

  function run(state, done) {
    let finished = false;
    const finish = () => {
      if (finished) return;
      finished = true;
      if (done) done();
    };
    try {
      const args = ["report", state, "--agent", "pi", "--pid", String(process.pid)];
      if (pane) args.push("--pane", pane);
      // detached: its own session, so the SIGHUP that ends a killed pane
      // doesn't take a late report with it.
      const child = spawn("herdmux", args, { stdio: "ignore", detached: true });
      child.on("error", finish);
      child.on("close", finish);
      child.unref();
    } catch {
      finish();
    }
  }

  function next() {
    const state = queue.shift();
    if (state === undefined) {
      busy = false;
      return;
    }
    busy = true;
    run(state, next);
  }

  function report(state) {
    if (exited || state === reported) return;
    reported = state;
    queue.push(state);
    if (!busy) next();
  }

  function update() {
    report(prompting ? "needs-input" : running ? "working" : "done");
  }

  pi.on("session_start", (_event, ctx) => {
    if (!active(ctx)) return;
    exited = false;
    pane = process.env.TMUX_PANE || "";
    update();
  });

  pi.on("agent_start", (_event, ctx) => {
    if (!active(ctx) || running) return;
    running = true;
    update();
  });

  pi.on("ui_prompt_start", (_event, ctx) => {
    if (!active(ctx)) return;
    prompting = true;
    update();
  });

  pi.on("ui_prompt_end", (_event, ctx) => {
    if (!active(ctx)) return;
    prompting = false;
    update();
  });

  pi.on("agent_settled", (_event, ctx) => {
    if (!active(ctx)) return;
    running = false;
    update();
  });

  pi.on("session_shutdown", (event, ctx) => {
    if (!active(ctx) || exited || !event || event.reason !== "quit") return;
    exited = true;
    // pi exits right after this: drop queued reports and send exit now.
    queue.length = 0;
    run("exit");
  });
}
