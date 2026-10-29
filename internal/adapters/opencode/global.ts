// Trace OpenCode v2 global plugin. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute, resolve } from "node:path";

const traceBin = __TRACE_BIN__;

export default {
  id: "usetrace.observer",
  async setup(ctx: any) {
    const cwd = ctx.location?.directory;
    if (typeof cwd !== "string" || !isAbsolute(cwd)) return;
    // Prompt admission proves the session belongs to this location. Enqueue
    // and delivery events identify which user input actually reached the agent.
    const sessions = new Map<string, { active: boolean; pending: Map<string, string>; tools: Map<string, { name: string; path: string }> }>();
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
      if (typeof session !== "string" || !session || sessions.has(session)) return;
      if (sessions.size >= 32) {
        const idle = [...sessions].find(([, state]) => !state.active && state.pending.size === 0);
        if (!idle) return;
        sessions.delete(idle[0]);
      }
      sessions.set(session, { active: false, pending: new Map(), tools: new Map() });
    });
    void (async () => {
      try {
        const types = ["session.inbox.enqueued", "session.inbox.delivered", "session.inbox.cancelled", "session.text.ended", "session.reasoning.ended", "session.tool.input.started", "session.tool.called", "session.tool.success", "session.tool.failed", "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted", "session.deleted"];
        for await (const ev of ctx.event.subscribe({ signal: controller.signal, types })) {
          const session = ev?.data?.sessionID;
          if (ev?.location?.directory && ev.location.directory !== cwd) continue;
          if (typeof session !== "string") continue;
          const state = sessions.get(session);
          if (!state) continue;
          const id = ev.data?.inboxID;
          if (ev.type === "session.inbox.enqueued") {
            const item = ev.data?.item;
            if (typeof id === "string" && item?.type === "user" && (state.pending.has(id) || state.pending.size < 16)) {
              const text = item.payload?.text;
              state.pending.set(id, typeof text === "string" && text.length <= 65536 ? text : "");
            }
          } else if (ev.type === "session.inbox.cancelled") {
            if (typeof id === "string") state.pending.delete(id);
          } else if (ev.type === "session.inbox.delivered") {
            if (typeof id !== "string" || !state.pending.has(id)) continue;
            const prompt = state.pending.get(id) ?? "";
            state.pending.delete(id);
            if (state.active) { send("tool_edit", session); send("turn_end", session); }
            state.tools.clear();
            send("session_start", session);
            send("turn_start", session, { prompt });
            state.active = true;
          } else if (state.active && (ev.type === "session.text.ended" || ev.type === "session.reasoning.ended")) {
            if (typeof ev.data.text === "string" && ev.data.text) {
              send("turn_context", session, { context_type: ev.type === "session.reasoning.ended" ? "thinking" : "response", text: ev.data.text.slice(0, 65536) });
            }
          } else if (state.active && ev.type.startsWith("session.tool.")) {
            const data = ev.data;
            if (typeof data.assistantMessageID !== "string" || typeof data.id !== "string") continue;
            const key = JSON.stringify([data.assistantMessageID, data.id]);
            if (ev.type === "session.tool.input.started") {
              if (typeof data.name === "string" && data.name.length <= 256 && (state.tools.has(key) || state.tools.size < 128)) {
                state.tools.set(key, { name: data.name.toLowerCase(), path: "" });
              }
            } else if (ev.type === "session.tool.called") {
              const tool = state.tools.get(key);
              const path = data.input?.filePath;
              if (tool && typeof path === "string" && path.length <= 4096) tool.path = resolve(cwd, path);
            } else if (ev.type === "session.tool.success" || ev.type === "session.tool.failed") {
              const tool = state.tools.get(key);
              state.tools.delete(key);
              if (ev.type !== "session.tool.success" || !tool) continue;
              if (tool.name === "read" && tool.path) send("file_read", session, { paths: [tool.path] });
              if (["edit", "write", "bash", "apply_patch"].includes(tool.name)) {
                send("tool_edit", session, { tool_name: tool.name, paths: ["edit", "write"].includes(tool.name) && tool.path ? [tool.path] : [] });
              }
            }
          } else if (["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"].includes(ev.type)) {
            if (state.active) { send("tool_edit", session); send("turn_end", session); state.active = false; state.tools.clear(); }
          } else if (ev.type === "session.deleted") {
            if (state.active) { send("tool_edit", session); send("turn_end", session); }
            send("session_end", session);
            sessions.delete(session);
            continue;
          }
          if (!state.active && state.pending.size === 0 && ev.type !== "session.inbox.enqueued") sessions.delete(session);
        }
      } catch (_) { /* An unavailable event stream must not affect OpenCode. */ }
    })();
    return () => controller.abort();
  },
};
