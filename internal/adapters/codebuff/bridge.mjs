// Trace Codebuff SDK bridge. Managed by trace agent add/remove.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";

const TRACE = __TRACE_BIN__;
const ROOT = __TRACE_ROOT__;

// Wrap explicit SDK client.run calls; the interactive CLI is not covered.
export function traceCodebuff(client) {
  const session = randomUUID();
  let pending = Promise.resolve();
  let queued = 0;
  let running = false;
  let flushers = 0;
  let current;

  function keepAlive(keep) {
    if (!current) return;
    const method = keep ? "ref" : "unref";
    current.child[method]();
    current.child.stdin[method]?.();
    current.timer[method]();
  }

  function send(kind, fields = {}) {
    if (queued >= 256) return false;
    queued++;
    pending = pending.then(() => new Promise((resolve) => {
      let settled = false;
      const done = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        if (current?.child === child) current = undefined;
        resolve();
      };
      let child;
      let timer;
      try {
        child = spawn(TRACE, ["--repo", ROOT, "hook", "codebuff", kind],
          { stdio: ["pipe", "ignore", "ignore"] });
        timer = setTimeout(() => { child.kill(); done(); }, 2000);
        current = { child, timer };
        if (!flushers) keepAlive(false);
        child.on("error", done);
        child.on("close", done);
        child.stdin.on("error", () => {});
        child.stdin.end(JSON.stringify({ session_id: session, ...fields }));
      } catch (_) { done(); }
    })).finally(() => { queued--; });
    return true;
  }

  return {
    async run(options) {
      // A Trace session has one open turn. Leave concurrent SDK calls intact
      // but do not assign them to the wrong turn.
      if (running || queued > 253 || typeof options?.prompt !== "string") {
        return client.run(options);
      }
      running = true;
      const prompt = options.prompt.length <= 65536 ? options.prompt : "";
      const recorded = send("turn_start", { prompt });
      try {
        return await client.run(options);
      } finally {
        if (recorded) {
          send("tool_edit"); // The SDK can modify files through custom tools.
          send("turn_end");
        }
        running = false;
      }
    },
    finish() {
      if (!running) send("session_end");
    },
    // Wait for events queued so far. Unlike the default fire-and-forget path,
    // this keeps Node alive while each hook process finishes or times out.
    flush() {
      flushers++;
      keepAlive(true);
      return pending.finally(() => {
        flushers--;
        if (!flushers) keepAlive(false);
      });
    },
  };
}
