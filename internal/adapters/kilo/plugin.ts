// Trace Kilo plugin. Managed by trace agent add/remove.
import { spawn } from "node:child_process";
import { realpathSync } from "node:fs";
import { isAbsolute, resolve } from "node:path";

const traceBin = __TRACE_BIN__;
const repoRoot = __TRACE_ROOT__;

const server = async (ctx: any = {}) => {
  // Kilo can load primary-checkout plugins in a linked worktree. A plugin
  // pinned to a different repository must not claim that worktree's events.
  if (typeof ctx.worktree === "string") {
    try {
      if (realpathSync(ctx.worktree) !== realpathSync(repoRoot)) return {};
    } catch (_) { return {}; }
  }
  ctx[Symbol.for("usetrace.kilo.project")] = true;
  let pending = Promise.resolve();
  let queued = 0;
  // Retain bounded completed text parts only for active turns in this workspace.
  const turns = new Map<string, { message: string; parts: Map<string, string> }>();
  function send(kind: string, sessionId: string, fields: Record<string, unknown> = {}) {
    if (!sessionId || queued >= 256) return;
    const payload = JSON.stringify({ session_id: sessionId, ...fields });
    queued++;
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
        child = spawn(traceBin, ["--repo", repoRoot, "hook", "kilo", kind], { stdio: ["pipe", "ignore", "ignore"] });
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
  return {
    "tool.execute.after": (input: any) => {
      if (typeof input?.sessionID !== "string" || typeof input.tool !== "string") return;
      const name = input.tool.toLowerCase();
      const file = input.args?.filePath;
      const base = (typeof ctx.directory === "string" && isAbsolute(ctx.directory) ? ctx.directory : repoRoot);
      const paths = typeof file === "string" && file.length <= 4096 ? [resolve(base, file)] : [];
      if (name === "read" && paths.length) send("file_read", input.sessionID, { paths });
      if (["edit", "write", "bash", "apply_patch"].includes(name)) {
        send("tool_edit", input.sessionID, { tool_name: name, paths: name === "edit" || name === "write" ? paths : [] });
      }
    },
    "chat.message": (input: any, output: any) => {
      const parts = Array.isArray(output?.parts) ? output.parts : [];
      const prompt = parts.filter((part: any) => part?.type === "text").map((part: any) => part.text).join("\n");
      if (typeof input.sessionID === "string" && (turns.has(input.sessionID) || turns.size < 16)) {
        turns.set(input.sessionID, { message: "", parts: new Map() });
      }
      send("turn_start", input.sessionID, { prompt: prompt.length <= 65536 ? prompt : "" });
    },
    event: ({ event }: any) => {
      const props = event.properties ?? {};
      switch (event.type) {
        case "session.created": send("session_start", props.info?.id); break;
        case "message.updated": {
          const info = props.info;
          const turn = turns.get(info?.sessionID);
          if (turn && info?.role === "assistant" && typeof info.id === "string" && turn.message !== info.id) {
            turn.message = info.id;
            turn.parts.clear();
          }
          break;
        }
        case "message.part.updated": {
          const part = props.part;
          const turn = turns.get(part?.sessionID);
          if (turn && part?.messageID === turn.message && part?.type === "text" && !part.ignored && typeof part.id === "string" && typeof part.text === "string" && (turn.parts.has(part.id) || turn.parts.size < 32)) {
            turn.parts.set(part.id, part.text.slice(0, 4096));
          }
          break;
        }
        case "session.idle":
          const turn = turns.get(props.sessionID);
          const response = turn ? [...turn.parts.values()].join("\n").slice(0, 65536) : "";
          if (response) send("turn_context", props.sessionID, { context_type: "response", text: response });
          turns.delete(props.sessionID);
          send("tool_edit", props.sessionID); // Unknown tools can write without a watcher event.
          send("turn_end", props.sessionID);
          break;
        case "session.deleted": {
          const sessionId = props.info?.id ?? props.sessionID;
          turns.delete(sessionId);
          send("session_end", sessionId);
          break;
        }
      }
    },
  };
};

export default { id: "trace", server };
