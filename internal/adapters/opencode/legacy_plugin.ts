// Trace OpenCode v1 global observer. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute, resolve } from "node:path";

const traceBin = __TRACE_BIN__;

export default async function traceObserver({ directory }: any) {
  if (typeof directory !== "string" || !isAbsolute(directory)) return {};
  const active = new Set<string>();
  let pending = Promise.resolve();
  let queued = 0;
  function send(kind: string, session: string, fields: Record<string, unknown> = {}) {
    if (!session || queued >= 256) return;
    const payload = JSON.stringify({ cwd: directory, session_id: session, ...fields });
    queued++;
    pending = pending.then(() => new Promise<void>((done) => {
      let timer: ReturnType<typeof setTimeout>;
      let settled = false;
      const finish = () => { if (!settled) { settled = true; clearTimeout(timer); done(); } };
      try {
        const child = spawn(traceBin, ["hook", "opencode-v1-global", kind], { stdio: ["pipe", "ignore", "ignore"] });
        timer = setTimeout(() => { child.kill(); finish(); }, 2000);
        child.unref(); timer.unref(); child.stdin.unref?.();
        child.on("error", finish); child.on("close", finish); child.stdin.on("error", () => {});
        child.stdin.end(payload);
      } catch (_) { finish(); }
    })).finally(() => { queued--; });
  }
  return {
    async "chat.message"(input: any, output: any) {
      const session = input?.sessionID;
      if (typeof session !== "string" || !session || active.has(session) || active.size >= 128) return;
      if (!Array.isArray(output?.parts)) return;
      const prompt = output.parts.filter((p: any) => p?.type === "text" && !p.synthetic && typeof p.text === "string").map((p: any) => p.text).join("\n").slice(0, 65536);
      active.add(session);
      send("session_start", session);
      send("turn_start", session, { prompt });
    },
    async "tool.execute.after"(input: any) {
      const session = input?.sessionID;
      if (typeof session !== "string" || !active.has(session) || typeof input.tool !== "string") return;
      const tool = input.tool.toLowerCase();
      const path = input.args?.filePath;
      const paths = typeof path === "string" && path.length <= 4096 ? [resolve(directory, path)] : [];
      if (tool === "read" && paths.length) send("file_read", session, { paths });
      if (["edit", "write", "bash", "apply_patch"].includes(tool)) send("tool_edit", session, { tool_name: tool, paths: ["edit", "write"].includes(tool) ? paths : [] });
    },
    async event({ event }: any) {
      const session = event?.properties?.sessionID;
      if (typeof session !== "string" || !active.has(session)) return;
      if (event.type === "session.idle" || (event.type === "session.status" && event.properties?.status?.type === "idle")) {
        active.delete(session);
        send("tool_edit", session);
        send("turn_end", session);
      }
    },
  };
}
