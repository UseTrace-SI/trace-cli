// Trace Pi/Oh My Pi global extension. Managed by trace setup.
import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const traceBin = __TRACE_BIN__;
const agentName = __TRACE_AGENT__;

export default function (pi: any) {
  let pending = Promise.resolve();
  let queued = 0;
  const argsByCall = new Map<string, { name: string; args: any }>();
  function send(kind: string, ctx: any, fields: Record<string, unknown> = {}) {
    let session: string;
    let projectTrusted = false;
    try {
      session = ctx.sessionManager.getSessionId();
      if (!session || !ctx.sessionManager.getSessionFile() || typeof ctx.cwd !== "string" || !isAbsolute(ctx.cwd)) return;
      if (typeof ctx.isProjectTrusted === "function") projectTrusted = ctx.isProjectTrusted() === true;
    } catch (_) { return; }
    if (queued >= 256) return;
    queued++;
    const payload = JSON.stringify({ session_id: session, cwd: ctx.cwd, project_trusted: projectTrusted, ...fields });
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
        child = spawn(traceBin, ["hook", agentName + "-global", kind], { stdio: ["pipe", "ignore", "ignore"] });
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
  pi.on("session_start", (_event: any, ctx: any) => send("session_start", ctx));
  pi.on("before_agent_start", (event: any, ctx: any) => {
    if (typeof event.prompt === "string") send("turn_start", ctx, { prompt: event.prompt.length <= 65536 ? event.prompt : "" });
  });
  pi.on("tool_execution_start", (event: any) => {
    if (argsByCall.size < 256) argsByCall.set(event.toolCallId, { name: event.toolName, args: event.args });
  });
  pi.on("tool_execution_end", (event: any, ctx: any) => {
    const call = argsByCall.get(event.toolCallId);
    argsByCall.delete(event.toolCallId);
    if (!call || event.isError) return;
    const file = call.args?.path ?? call.args?.file_path;
    const fields = { tool_name: call.name, paths: typeof file === "string" ? [file] : [] };
    if (["write", "edit", "apply_patch", "bash", "execute"].includes(call.name)) send("tool_edit", ctx, fields);
    if (call.name === "read") send("file_read", ctx, fields);
  });
  pi.on("agent_end", (event: any, ctx: any) => {
    if (event.willContinue || event.awaitingAsyncWork) return;
    const messages = Array.isArray(event.messages) ? event.messages : [];
    const assistant = [...messages].reverse().find((message: any) => message?.role === "assistant");
    const text = Array.isArray(assistant?.content)
      ? assistant.content.filter((part: any) => part?.type === "text").map((part: any) => part.text).join("\n")
      : typeof assistant?.content === "string" ? assistant.content : "";
    if (text) send("turn_context", ctx, { context_type: "response", text: text.slice(0, 65536) });
    send("tool_edit", ctx); // Custom tools or interrupted commands may write files.
    send("turn_end", ctx);
  });
  pi.on("session_shutdown", (_event: any, ctx: any) => send("session_end", ctx));
}
