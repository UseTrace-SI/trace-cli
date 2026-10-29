// Trace Amp plugin. Managed by trace agent add/remove.
import { spawn } from "node:child_process";

const traceBin = __TRACE_BIN__;
const repoRoot = __TRACE_ROOT__;

export default function (amp: any) {
  let pending = Promise.resolve();
  function send(kind: string, sessionId: string, fields: Record<string, unknown> = {}) {
    if (!sessionId) return;
    const payload = JSON.stringify({ session_id: sessionId, ...fields });
    pending = pending.then(() => new Promise<void>((resolve) => {
      const child = spawn(traceBin, ["--repo", repoRoot, "hook", "amp", kind], { stdio: ["pipe", "ignore", "ignore"] });
      child.on("error", () => resolve());
      child.on("close", () => resolve());
      child.stdin.on("error", () => {});
      child.stdin.end(payload);
    }));
  }

  amp.on("session.start", (event: any) => send("session_start", event.thread?.id));
  amp.on("agent.start", (event: any) => send("turn_start", event.thread?.id, { prompt: event.message }));
  amp.on("agent.end", (event: any) => {
    send("turn_end", event.thread?.id);
  });
}
