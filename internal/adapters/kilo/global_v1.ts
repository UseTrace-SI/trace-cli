// Trace Kilo global plugin. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;

const server = async ({ directory }: any) => {
  if (typeof directory !== "string" || !isAbsolute(directory)) return {};
  let pending = Promise.resolve();
  let queued = 0;
  function send(kind: string, sessionId: string, fields: Record<string, unknown> = {}) {
    if (!sessionId || queued >= 256) return;
    const payload = JSON.stringify({ session_id: sessionId, cwd: directory, ...fields });
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
      send("turn_start", input.sessionID, { prompt: prompt.length <= 65536 ? prompt : "" });
    },
    event: ({ event }: any) => {
      const props = event.properties ?? {};
      switch (event.type) {
        case "session.created": send("session_start", props.info?.id); break;
        case "session.idle":
          send("tool_edit", props.sessionID); // Unknown tools can write without a watcher event.
          send("turn_end", props.sessionID);
          break;
        case "session.deleted": send("session_end", props.info?.id ?? props.sessionID); break;
      }
    },
  };
};

export default { id: "trace-global", server };
