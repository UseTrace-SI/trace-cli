// Trace OpenCode v2 global plugin. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;

export default {
  id: "usetrace.observer",
  async setup(ctx: any) {
    const cwd = ctx.location?.directory;
    if (typeof cwd !== "string" || !isAbsolute(cwd)) return;
    const active = new Set<string>();
    const controller = new AbortController();
    let pending = Promise.resolve();
    let queued = 0;
    function send(kind: string, session: string, fields: Record<string, unknown> = {}) {
      if (!session || queued >= 256) return;
      const payload = JSON.stringify({ cwd, session_id: session, ...fields });
      queued++;
      pending = pending.then(() => new Promise<void>((resolve) => {
        let timer: ReturnType<typeof setTimeout>;
        let settled = false;
        const done = () => { if (!settled) { settled = true; clearTimeout(timer); resolve(); } };
        try {
          const child = spawn(traceBin, ["hook", "opencode-global", kind], { stdio: ["pipe", "ignore", "ignore"] });
          timer = setTimeout(() => { child.kill(); done(); }, 2000);
          child.unref(); timer.unref(); child.stdin.unref?.();
          child.on("error", done); child.on("close", done); child.stdin.on("error", () => {});
          child.stdin.end(payload);
        } catch (_) { done(); }
      })).finally(() => { queued--; });
    }
    await ctx.session.hook("prompt", (input: any) => {
      const session = input?.sessionID;
      if (typeof session !== "string" || !session || active.has(session) || active.size >= 128) return;
      active.add(session);
      send("session_start", session);
      const prompt = typeof input.prompt?.text === "string" && input.prompt.text.length <= 65536 ? input.prompt.text : "";
      send("turn_start", session, { prompt });
    });
    void (async () => {
      try {
        for await (const ev of ctx.event.subscribe({ signal: controller.signal, types: ["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"] })) {
          const session = ev?.data?.sessionID;
          if (ev?.location?.directory && ev.location.directory !== cwd) continue;
          if (typeof session !== "string" || !active.has(session)) continue;
          if (!["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"].includes(ev.type)) continue;
          active.delete(session);
          send("tool_edit", session);
          send("turn_end", session);
        }
      } catch (_) { /* An unavailable event stream must not affect OpenCode. */ }
    })();
    return () => controller.abort();
  },
};
