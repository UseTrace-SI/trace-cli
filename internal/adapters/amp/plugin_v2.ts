// Trace Amp plugin. Managed by trace agent add/remove.
import { spawn } from "node:child_process";

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
  amp.on("agent.end", (event: any) => {
    send("tool_edit", event.thread?.id);
    send("turn_end", event.thread?.id);
  });
}
