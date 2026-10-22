// node --test for the opencode plugin. A fake plugin host
// calls server() and fires the event sequences observed from opencode 1.18.35;
// a fake `herdmux` first on PATH records every report's argv.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import plugin from "./herdmux-agent-state.js";

const ROOT = "ses_root";
const CHILD = "ses_child";

// start runs the plugin's server() as opencode would, with the given argv
// (after opencode's binary) and environment overrides. The plugin snapshots
// its environment, so process.env and process.argv are restored right away.
async function start({ args = [], env = {}, fake = true } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "herdmux-opencode-test-"));
  const log = path.join(dir, "reports");
  if (fake) {
    fs.writeFileSync(path.join(dir, "herdmux"), '#!/bin/sh\nprintf "%s\\n" "$*" >> "$FAKE_HERDMUX_LOG"\n', {
      mode: 0o755,
    });
  }
  const saved = { env: process.env, argv: process.argv };
  process.env = {
    PATH: `${dir}:/usr/bin:/bin`,
    FAKE_HERDMUX_LOG: log,
    HERDMUX: "1",
    TMUX_PANE: "%3",
    ...env,
  };
  for (const [k, v] of Object.entries(process.env)) if (v === undefined) delete process.env[k];
  process.argv = ["bun", "/$bunfs/root/opencode", ...args];
  let hooks;
  try {
    hooks = await plugin.server({});
  } finally {
    process.env = saved.env;
    process.argv = saved.argv;
  }
  const lines = () => (fs.existsSync(log) ? fs.readFileSync(log, "utf8").split("\n").filter(Boolean) : []);
  return {
    hooks,
    chat: (sessionID) => hooks["chat.message"]({ sessionID, agent: "build" }, { message: {}, parts: [] }),
    ev: (type, properties) => hooks.event({ event: { type, properties } }),
    // states waits until at least n reports arrived (then a little longer, to
    // catch extras) and returns the reported states, checking each argv.
    async states(n) {
      const deadline = Date.now() + 5000;
      while (lines().length < n && Date.now() < deadline) await sleep(10);
      await sleep(150);
      return lines().map((l) => {
        const m = l.match(/^report (\S+) --agent opencode --pid (\d+)$/);
        assert.ok(m, `unexpected report argv: ${l}`);
        assert.equal(Number(m[2]), process.pid, "--pid is process.pid");
        return m[1];
      });
    },
    lines,
  };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const status = (sessionID, type) => ["session.status", { sessionID, status: { type } }];
const created = (id, parentID) => ["session.created", { sessionID: id, info: { id, parentID } }];

async function fire(p, steps) {
  for (const s of steps) {
    if (s[0] === "chat") await p.chat(s[1]);
    else await p.ev(s[0], s[1]);
  }
}

const prompt = (sid = ROOT) => [created(sid), ["chat", sid], status(sid, "busy"), status(sid, "busy")];
const finish = (sid = ROOT) => [status(sid, "busy"), status(sid, "idle"), ["session.idle", { sessionID: sid }]];

test("registers as done at startup", async () => {
  const p = await start();
  assert.deepEqual(await p.states(1), ["done"]);
});

test("user prompt, then the turn finishes; busy repeats are deduped", async () => {
  const p = await start();
  await fire(p, [...prompt(), status(ROOT, "busy"), ["message.part.updated", { sessionID: ROOT }], ...finish()]);
  assert.deepEqual(await p.states(3), ["done", "working", "done"]);
});

test("permission asked, then allowed: needs-input wins over busy", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["permission.asked", { id: "per_1", sessionID: ROOT, permission: "bash" }],
    status(ROOT, "busy"),
    status(ROOT, "busy"),
    ["permission.replied", { sessionID: ROOT, requestID: "per_1", reply: "once" }],
    status(ROOT, "busy"),
    ...finish(),
  ]);
  assert.deepEqual(await p.states(5), ["done", "working", "needs-input", "working", "done"]);
});

test("permission rejected with Esc", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["permission.asked", { id: "per_1", sessionID: ROOT }],
    ["permission.replied", { sessionID: ROOT, requestID: "per_1", reply: "reject" }],
    status(ROOT, "idle"),
    ["session.idle", { sessionID: ROOT }],
  ]);
  assert.deepEqual(await p.states(5), ["done", "working", "needs-input", "working", "done"]);
});

test("question, then replied; a rejected question also unblocks", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["question.asked", { id: "que_1", sessionID: ROOT }],
    status(ROOT, "busy"),
    ["question.replied", { sessionID: ROOT, requestID: "que_1" }],
    ["question.asked", { id: "que_2", sessionID: ROOT }],
    ["question.rejected", { sessionID: ROOT, requestID: "que_2" }],
    ...finish(),
  ]);
  assert.deepEqual(await p.states(7), ["done", "working", "needs-input", "working", "needs-input", "working", "done"]);
});

test("two pending blockers: needs-input until both are answered", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["permission.asked", { id: "per_1", sessionID: ROOT }],
    ["question.asked", { id: "que_1", sessionID: ROOT }],
    ["permission.replied", { sessionID: ROOT, requestID: "per_1" }],
    status(ROOT, "busy"),
    ["question.replied", { sessionID: ROOT, requestID: "que_1" }],
    ...finish(),
  ]);
  assert.deepEqual(await p.states(5), ["done", "working", "needs-input", "working", "done"]);
});

test("subagent: a child's permission blocks, its mid-turn idle is ignored", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    created(CHILD, ROOT),
    ["session.updated", { sessionID: CHILD, info: { id: CHILD, parentID: ROOT } }],
    ["chat", CHILD],
    status(CHILD, "busy"),
    ["permission.asked", { id: "per_c", sessionID: CHILD }],
    ["permission.replied", { sessionID: CHILD, requestID: "per_c" }],
    status(CHILD, "busy"),
    status(CHILD, "idle"),
    ["session.idle", { sessionID: CHILD }],
    status(ROOT, "busy"),
    ...finish(),
  ]);
  assert.deepEqual(await p.states(5), ["done", "working", "needs-input", "working", "done"]);
});

test("a child idle alone never produces done while the root is busy", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    created(CHILD, ROOT),
    ["chat", CHILD],
    status(CHILD, "idle"),
    ["session.idle", { sessionID: CHILD }],
  ]);
  assert.deepEqual(await p.states(2), ["done", "working"]);
});

test("interrupt: MessageAbortedError, then idle, gives done", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["session.error", { sessionID: ROOT, error: { name: "MessageAbortedError", data: {} } }],
    status(ROOT, "idle"),
    ["session.idle", { sessionID: ROOT }],
  ]);
  assert.deepEqual(await p.states(3), ["done", "working", "done"]);
});

test("root idle drops a pending blocker", async () => {
  const p = await start();
  await fire(p, [...prompt(), ["permission.asked", { id: "per_1", sessionID: ROOT }], ...finish()]);
  assert.deepEqual(await p.states(4), ["done", "working", "needs-input", "done"]);
});

test("/new while busy: the old session still drives the pane", async () => {
  const p = await start();
  const NEW = "ses_new";
  await fire(p, [
    ...prompt(),
    created(NEW),
    ["session.updated", { sessionID: NEW, info: { id: NEW } }],
    status(ROOT, "busy"),
    ...finish(),
    // A prompt in the new session while the old one is busy again: done only
    // once both roots are idle.
    ["chat", ROOT],
    status(ROOT, "busy"),
    ["chat", NEW],
    status(NEW, "busy"),
    ...finish(ROOT),
    ...finish(NEW),
  ]);
  assert.deepEqual(await p.states(5), ["done", "working", "done", "working", "done"]);
});

test("session.deleted forgets the session and its blockers", async () => {
  const p = await start();
  await fire(p, [
    ...prompt(),
    ["permission.asked", { id: "per_1", sessionID: ROOT }],
    ["session.deleted", { sessionID: ROOT, info: { id: ROOT } }],
  ]);
  assert.deepEqual(await p.states(4), ["done", "working", "needs-input", "done"]);
});

test("reports for a full TUI, run and --mini", async () => {
  for (const args of [[], ["."], ["run", "hello"], ["--mini"], ["--log-level", "DEBUG", "run", "x"]]) {
    const p = await start({ args });
    await fire(p, prompt());
    assert.deepEqual(await p.states(2), ["done", "working"], `argv ${JSON.stringify(args)}`);
  }
});

test("gate: inert without HERDMUX=1 or TMUX_PANE, and for serve/web/attach", async () => {
  const cases = [
    { env: { HERDMUX: undefined } },
    { env: { HERDMUX: "0" } },
    { env: { TMUX_PANE: undefined } },
    { env: { TMUX_PANE: "" } },
    { args: ["serve"] },
    { args: ["--print-logs", "serve", "--port", "4096"] },
    { args: ["web"] },
    { args: ["attach", "http://localhost:4096"] },
    { args: ["--attach=http://localhost:4096"] },
  ];
  for (const c of cases) {
    const p = await start(c);
    assert.deepEqual(p.hooks, {}, `no hooks for ${JSON.stringify(c)}`);
    await sleep(100);
    assert.deepEqual(p.lines(), [], `no reports for ${JSON.stringify(c)}`);
  }
});

test("a missing or failing herdmux never throws into opencode", async () => {
  const p = await start({ fake: false });
  await fire(p, [...prompt(), ["permission.asked", { id: "per_1", sessionID: ROOT }], ...finish()]);
  await p.hooks.event({});
  await p.hooks.event({ event: { type: "session.status" } });
  await p.hooks["chat.message"](undefined);
  await sleep(100);
  assert.deepEqual(p.lines(), []);
});
