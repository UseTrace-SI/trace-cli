// Trace Amp global plugin. Managed by trace setup --agent amp.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;

export default function (amp: any) {
  let pending = Promise.resolve();
  let queued = 0;
  let workspace = "";
  try {
    if (amp.system.executor.kind === "local" && amp.system.workspaceRoot) {
      const candidate = amp.helpers.filePathFromURI(amp.system.workspaceRoot);
      if (typeof candidate === "string" && isAbsolute(candidate)) workspace = candidate;
    }
  } catch (_) { /* Ignore remote or unavailable workspace identities. */ }

  function send(kind: string, sessionId: unknown, fields: Record<string, unknown> = {}) {
    if (!workspace || typeof sessionId !== "string" || !sessionId || queued >= 256) return;
    queued++;
    const payload = JSON.stringify({ cwd: workspace, session_id: sessionId, ...fields });
    pending = pending.then(() => new Promise<void>((resolve) => {
      let child: ReturnType<typeof spawn>;
      let timer: ReturnType<typeof setTimeout>;
      let settled = false;
      const done = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve();
      };
      try {
        child = spawn(traceBin, ["hook", "amp-global", kind], { stdio: ["pipe", "ignore", "ignore"] });
        timer = setTimeout(() => { child.kill(); done(); }, 2000);
        child.unref();
        timer.unref();
        child.stdin.unref?.();
        child.on("error", done);
        child.on("close", done);
        child.stdin.on("error", () => {});
        child.stdin.end(payload);
      } catch (_) { done(); }
    })).finally(() => { queued--; });
  }

  amp.on("session.start", (event: any) => send("session_start", event.thread?.id));
  amp.on("agent.start", (event: any) => {
    const prompt = typeof event.message === "string" && event.message.length <= 65536 ? event.message : "";
    send("turn_start", event.thread?.id, { prompt });
  });
  amp.on("agent.end", (event: any) => {
    const last = Array.isArray(event.messages) ? event.messages.at(-1) : null;
    if (last?.role === "assistant" && Array.isArray(last.content)) {
      let text = "";
      for (const block of last.content) {
        if (block?.type === "text" && typeof block.text === "string" && text.length < 65536) {
          text += (text ? "\n" : "") + block.text.slice(0, 65536 - text.length);
        }
      }
      if (text) send("turn_context", event.thread?.id, { context_type: "response", text: text.slice(0, 65536) });
    }
    // Tools and shell commands may modify files without reporting their paths.
    send("tool_edit", event.thread?.id);
    send("turn_end", event.thread?.id);
  });
}
