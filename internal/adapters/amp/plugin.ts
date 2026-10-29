// Trace Amp plugin. Managed by trace agent add/remove.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;
const repoRoot = __TRACE_ROOT__;

export default function (amp: any) {
  let pending = Promise.resolve();
  let queued = 0;
  function send(kind: string, sessionId: unknown, fields: Record<string, unknown> = {}) {
    if (typeof sessionId !== "string" || !sessionId || queued >= 256) return;
    queued++;
    const payload = JSON.stringify({ session_id: sessionId, ...fields });
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
        child = spawn(traceBin, ["--repo", repoRoot, "hook", "amp", kind], { stdio: ["pipe", "ignore", "ignore"] });
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
  amp.on("tool.result", (event: any) => {
    if (event?.status !== "done" || typeof event.tool !== "string" || event.tool.length > 256) return;
    try {
      const files = amp.helpers.filesModifiedByToolCall(event);
      if (!Array.isArray(files)) return;
      const paths = files.slice(0, 64).map((file: any) => amp.helpers.filePathFromURI(file))
        .filter((path: unknown) => typeof path === "string" && path.length <= 4096 && isAbsolute(path));
      if (paths.length) send("tool_edit", event.thread?.id, { tool_name: event.tool, paths });
    } catch (_) { /* The turn-end scan remains available when helpers fail. */ }
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
    send("tool_edit", event.thread?.id);
    send("turn_end", event.thread?.id);
  });
}
