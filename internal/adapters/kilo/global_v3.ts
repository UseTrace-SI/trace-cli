// Trace Kilo global plugin. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;

const server = async (ctx: any) => {
  const { directory } = ctx;
  if (typeof directory !== "string" || !isAbsolute(directory)) return {};
  let pending = Promise.resolve();
  let queued = 0;
  // Retain bounded completed text parts only for active turns in this workspace.
  const turns = new Map<string, { message: string; parts: Map<string, string> }>();
  function send(kind: string, sessionId: string, fields: Record<string, unknown> = {}) {
    if (!sessionId || queued >= 256) return;
    const payload = JSON.stringify({ session_id: sessionId, cwd: directory, project_loaded: ctx[Symbol.for("usetrace.kilo.project")] === true, ...fields });
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
        child = spawn(traceBin, ["hook", "kilo-global", kind], { stdio: ["pipe", "ignore", "ignore"] });
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

export default { id: "trace-global", server };
