// herdmux agent state for opencode (a server plugin).
// Installed by `herdmux install`; reinstalling overwrites this file.
// HERDMUX_INTEGRATION_ID=opencode
// HERDMUX_INTEGRATION_VERSION=1
//
// Maps opencode's events to one pane-level agent state and runs
// `herdmux report <working|needs-input|done> --agent opencode --pid <pid>`
// on transitions only. Inert unless HERDMUX=1 and TMUX_PANE are set, and for
// `opencode serve`, `web` and `attach`, whose server does not belong to the
// pane. Exit is not reported: herdmux's reconcile drops the agent once the
// pid is gone or no longer owns the pane. Plain JS, no dependencies.

import { spawn } from "node:child_process";

const AGENT = "opencode";
const REPORT_TIMEOUT_MS = 5000;

// The first positional argument is opencode's subcommand. Full TUIs, `run`
// and `--mini` host the server in the pane's own process; these don't.
function ownsPane(argv) {
  const args = argv.slice(2);
  const end = args.indexOf("--");
  if (end !== -1) args.splice(end);
  if (args.some((a) => a === "--attach" || a.startsWith("--attach="))) return false;
  const sub = args.find((a) => !a.startsWith("-"));
  return !["serve", "web", "attach"].includes(sub);
}

function str(v) {
  return typeof v === "string" && v ? v : undefined;
}

function statusKind(status) {
  const kind = typeof status === "string" ? status : status?.type;
  return typeof kind === "string" ? kind.toLowerCase() : undefined;
}

// createTracker holds the pane-level state machine, aggregated over every
// root and child session this process hosts. handle* return nothing; state()
// gives the derived state.
function createTracker() {
  const parentOf = new Map(); // sessionID -> parentID (children only)
  const busyRoots = new Set();
  const blockers = new Map(); // request id -> sessionID

  const rootOf = (id) => {
    const seen = new Set();
    while (parentOf.has(id) && !seen.has(id)) {
      seen.add(id);
      id = parentOf.get(id);
    }
    return id;
  };
  const isRoot = (id) => !parentOf.has(id);

  const dropBlockers = (pred) => {
    for (const [req, sid] of blockers) if (pred(sid)) blockers.delete(req);
  };

  return {
    chatMessage(sessionID) {
      if (sessionID) busyRoots.add(rootOf(sessionID));
    },
    event(type, p) {
      const sessionID = str(p.sessionID);
      const info = p.info;
      if ((type === "session.created" || type === "session.updated") && str(info?.id)) {
        if (str(info.parentID)) parentOf.set(info.id, info.parentID);
        return;
      }
      switch (type) {
        case "session.status": {
          if (!sessionID || !isRoot(sessionID)) return;
          const kind = statusKind(p.status);
          if (kind === "busy" || kind === "retry") busyRoots.add(sessionID);
          else if (kind === "idle") this.rootIdle(sessionID);
          return;
        }
        case "session.idle":
          // Child idles fire mid-turn; only a root's idle ends the turn.
          if (sessionID && isRoot(sessionID)) this.rootIdle(sessionID);
          return;
        case "permission.asked":
        case "question.asked": {
          const id = str(p.id);
          if (id) blockers.set(id, sessionID);
          return;
        }
        case "permission.replied":
        case "question.replied":
        case "question.rejected": {
          const id = str(p.requestID) ?? str(p.id);
          if (id) blockers.delete(id);
          return;
        }
        case "session.deleted": {
          const id = str(info?.id) ?? sessionID;
          if (!id) return;
          busyRoots.delete(id);
          dropBlockers((sid) => sid === id);
          parentOf.delete(id);
          return;
        }
        default:
          // session.error is always followed by idle, which produces done.
          return;
      }
    },
    rootIdle(root) {
      busyRoots.delete(root);
      dropBlockers((sid) => sid !== undefined && rootOf(sid) === root);
    },
    state() {
      if (blockers.size > 0) return "needs-input";
      if (busyRoots.size > 0) return "working";
      return "done";
    },
  };
}

// createReporter runs `herdmux report` without blocking the plugin: calls are
// queued so they reach herdmux in order, each child is detached and ignored on
// failure, and a hung one is killed after REPORT_TIMEOUT_MS.
function createReporter(env) {
  let chain = Promise.resolve();
  const runOnce = (state) =>
    new Promise((resolve) => {
      let child;
      try {
        child = spawn("herdmux", ["report", state, "--agent", AGENT, "--pid", String(process.pid)], {
          env,
          detached: true,
          stdio: "ignore",
        });
      } catch {
        resolve();
        return;
      }
      const timer = setTimeout(() => {
        try {
          child.kill("SIGKILL");
        } catch {}
        resolve();
      }, REPORT_TIMEOUT_MS);
      timer.unref?.();
      const finish = () => {
        clearTimeout(timer);
        resolve();
      };
      child.on("error", finish);
      child.on("exit", finish);
      child.unref();
    });
  return (state) => {
    chain = chain.then(() => runOnce(state)).catch(() => {});
  };
}

const HerdmuxAgentStatePlugin = async () => {
  const env = { ...process.env };
  if (env.HERDMUX !== "1" || !env.TMUX_PANE || !ownsPane(process.argv)) return {};

  const tracker = createTracker();
  const report = createReporter(env);
  // Register at startup as done (idle, the user's move), like claude's
  // SessionStart; registration never raises Attention.
  let last = "done";
  report(last);
  const update = () => {
    const next = tracker.state();
    if (next !== last) {
      last = next;
      report(next);
    }
  };

  return {
    "chat.message": async (input) => {
      try {
        tracker.chatMessage(str(input?.sessionID));
        update();
      } catch {}
    },
    event: async ({ event } = {}) => {
      try {
        if (!str(event?.type)) return;
        tracker.event(event.type, event.properties ?? {});
        update();
      } catch {}
    },
  };
};

// Only a default export: opencode treats exported functions as plugins, and a
// module must export either server() or tui(), never both.
export default {
  id: "herdmux.opencode",
  server: HerdmuxAgentStatePlugin,
};
