// node --test suite for herdmux-agent-state.ts, with a fake pi host and a fake
// `herdmux` on PATH that records its arguments. No real pi, no tmux.
//
// The extension is plain JavaScript in a .ts file, so it is copied to a temp
// .mjs and imported: that works under node 20 (which can't load .ts) and 22.

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "herdmux-pi-test-"));
process.on("exit", () => fs.rmSync(tmp, { recursive: true, force: true }));

const copy = path.join(tmp, "herdmux-agent-state.mjs");
fs.copyFileSync(path.join(here, "herdmux-agent-state.ts"), copy);
const { default: extension } = await import(pathToFileURL(copy).href);

const bin = path.join(tmp, "bin");
fs.mkdirSync(bin);
fs.writeFileSync(
  path.join(bin, "herdmux"),
  '#!/bin/sh\nprintf "%s\\n" "$*" >> "$HERDMUX_FAKE_LOG"\n',
  { mode: 0o755 },
);

const baseEnv = { ...process.env };
let log;
let n = 0;

beforeEach(() => {
  log = path.join(tmp, `log-${n++}`);
  for (const k of Object.keys(process.env)) delete process.env[k];
  Object.assign(process.env, baseEnv, {
    PATH: `${bin}:${baseEnv.PATH}`,
    HERDMUX: "1",
    TMUX_PANE: "%7",
    HERDMUX_FAKE_LOG: log,
  });
  delete process.env.PI_SUBAGENT_CHILD;
});

// host is a fake pi: on() records handlers, fire() calls them like pi does.
function host(mode = "tui") {
  const handlers = new Map();
  const pi = {
    on(event, handler) {
      if (!handlers.has(event)) handlers.set(event, []);
      handlers.get(event).push(handler);
    },
  };
  extension(pi);
  const ctx = { mode, hasUI: mode === "tui", ui: {} };
  return {
    async fire(type, extra = {}) {
      for (const h of handlers.get(type) ?? []) await h({ type, ...extra }, ctx);
    },
    async seq(...events) {
      for (const e of events) {
        if (typeof e === "string") await this.fire(e);
        else await this.fire(e.type, e);
      }
    },
  };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function calls() {
  try {
    return fs.readFileSync(log, "utf8").split("\n").filter(Boolean);
  } catch {
    return [];
  }
}

// reports waits for the fake herdmux to have recorded count calls, then a
// little longer so extra calls would show up, and returns the reported states.
async function reports(count) {
  const deadline = Date.now() + 5000;
  while (calls().length < count && Date.now() < deadline) await sleep(10);
  await sleep(150);
  const got = calls();
  for (const c of got) {
    assert.equal(c, `report ${c.split(" ")[1]} --agent pi --pid ${process.pid} --pane %7`);
  }
  return got.map((c) => c.split(" ")[1]);
}

const start = { type: "session_start", reason: "startup" };

test("prompt then settle: done, working, done", async () => {
  const h = host();
  await h.seq(start, "agent_start", "agent_end", "agent_settled");
  assert.deepEqual(await reports(3), ["done", "working", "done"]);
});

test("a run with several agent_start/agent_end pairs reports working once", async () => {
  const h = host();
  await h.seq(start, "agent_start", "agent_end", "agent_start", "agent_end", "agent_start", "agent_end");
  await h.fire("agent_settled");
  assert.deepEqual(await reports(3), ["done", "working", "done"]);
});

test("ui prompt during a run returns to working", async () => {
  const h = host();
  await h.seq(start, "agent_start", { type: "ui_prompt_start", kind: "confirm" }, { type: "ui_prompt_end", kind: "confirm" }, "agent_settled");
  assert.deepEqual(await reports(5), ["done", "working", "needs-input", "working", "done"]);
});

test("ui prompt while idle returns to done", async () => {
  const h = host();
  await h.seq(start, { type: "ui_prompt_start", kind: "custom" }, { type: "ui_prompt_end", kind: "custom" });
  assert.deepEqual(await reports(3), ["done", "needs-input", "done"]);
});

test("an aborted or failed run still settles to done", async () => {
  const h = host();
  await h.seq(start, "agent_start", "turn_start", "turn_end", "agent_end");
  await h.fire("agent_before_settle", { outcome: "error" });
  await h.fire("agent_settled");
  assert.deepEqual(await reports(3), ["done", "working", "done"]);
});

test("triggerTurn after a settle: done, working, done", async () => {
  const h = host();
  await h.seq(start, "agent_start", "agent_end", "agent_settled", "agent_start", "agent_end", "agent_settled");
  assert.deepEqual(await reports(5), ["done", "working", "done", "working", "done"]);
});

test("shutdown quit reports exit once", async () => {
  const h = host();
  await h.seq(start, "agent_start", "agent_settled");
  await reports(3);
  await h.fire("session_shutdown", { reason: "quit" });
  await h.fire("session_shutdown", { reason: "quit" });
  assert.deepEqual(await reports(4), ["done", "working", "done", "exit"]);
});

test("shutdown reload is not an exit", async () => {
  const h = host();
  await h.seq(start);
  await h.fire("session_shutdown", { reason: "reload" });
  await h.fire("session_start", { reason: "reload" });
  await h.fire("agent_start");
  assert.deepEqual(await reports(2), ["done", "working"]);
});

const gates = {
  "PI_SUBAGENT_CHILD=1": () => (process.env.PI_SUBAGENT_CHILD = "1"),
  "ctx.mode print": null,
  "no HERDMUX": () => delete process.env.HERDMUX,
  "no TMUX_PANE": () => delete process.env.TMUX_PANE,
};
for (const [name, setup] of Object.entries(gates)) {
  test(`gate: ${name} reports nothing`, async () => {
    if (setup) setup();
    const h = host(setup ? "tui" : "print");
    await h.seq(start, "agent_start", { type: "ui_prompt_start" }, { type: "ui_prompt_end" }, "agent_settled");
    await h.fire("session_shutdown", { reason: "quit" });
    assert.deepEqual(await reports(0), []);
  });
}

test("a missing herdmux doesn't throw or block", async () => {
  process.env.PATH = path.join(tmp, "empty");
  const h = host();
  await h.seq(start, "agent_start", "agent_settled");
  await h.fire("session_shutdown", { reason: "quit" });
  assert.deepEqual(await reports(0), []);
});
