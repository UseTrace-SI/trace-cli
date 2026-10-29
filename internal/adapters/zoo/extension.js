// Trace Zoo Code task observer. It never changes Zoo Code's execution.
const vscode = require('vscode');
const path = require('node:path');
const { spawn } = require('node:child_process');
const TRACE = __TRACE_BIN__;

let disposeObserver = () => {};
let stopped = false;

function activate() {
  stopped = false;
  const zoo = vscode.extensions.getExtension('ZooCodeOrganization.zoo-code');
  if (!zoo) return;
  // Do not hold up VS Code activation while Zoo Code starts.
  Promise.resolve().then(() => zoo.activate()).then((api) => {
    if (stopped || !api || typeof api.on !== 'function' || typeof api.getTaskHistoryItem !== 'function') return;
    const active = new Map();
    const listeners = [];
    let pending = Promise.resolve();
    let queued = 0;
    let disposed = false;

    function enqueue(fn) {
      if (disposed || queued >= 128) return;
      queued++;
      pending = pending.then(() => { if (!disposed) return fn(); }).catch(() => {}).finally(() => { queued--; });
    }
    function send(kind, payload) {
      return new Promise((resolve) => {
        let child;
        let timer;
        let finished = false;
        const done = () => {
          if (finished) return;
          finished = true;
          clearTimeout(timer);
          resolve();
        };
        try {
          child = spawn(TRACE, ['hook', 'zoo', kind], { stdio: ['pipe', 'ignore', 'ignore'] });
          timer = setTimeout(() => { child.kill(); done(); }, 2000);
          timer.unref();
          child.unref();
          child.stdin.unref?.();
          child.on('error', done);
          child.on('close', done);
          child.stdin.on('error', () => {});
          child.stdin.end(JSON.stringify(payload));
        } catch (_) { done(); }
      });
    }
    function allowedWorkspace(workspace) {
      if (typeof workspace !== 'string' || !path.isAbsolute(workspace)) return false;
      return (vscode.workspace.workspaceFolders || []).some(
        (folder) => folder.uri.scheme === 'file' && path.resolve(folder.uri.fsPath) === path.resolve(workspace),
      );
    }
    async function start(taskId) {
      if (typeof taskId !== 'string' || !taskId || active.has(taskId)) return;
      const item = await api.getTaskHistoryItem(taskId);
      // Unknown workspace and subtasks are intentionally skipped.
      if (!item || item.parentTaskId || !allowedWorkspace(item.workspace)) return;
      const payload = { cwd: item.workspace, session_id: taskId };
      active.set(taskId, { payload, phase: 'running', result: '' });
      const prompt = typeof item.task === 'string' && item.task.length <= 65536 ? item.task : '';
      await send('turn_start', { ...payload, prompt });
    }
    async function endTurn(state) {
      // Task tools may have changed files without reporting paths.
      await send('tool_edit', state.payload);
      await send('turn_end', { ...state.payload, text: state.result });
      state.phase = 'idle';
      state.result = '';
    }
    async function finish(taskId) {
      const state = active.get(taskId);
      if (!state) return;
      if (state.phase === 'running') await endTurn(state);
      active.delete(taskId);
      await send('session_end', state.payload);
    }
    async function message(event) {
      const state = active.get(event.taskId);
      const msg = event.message;
      if (!state || !msg || msg.partial === true) return;
      if (state.phase === 'running' && msg.type === 'say' && msg.say === 'completion_result') {
        state.result = typeof msg.text === 'string' && msg.text.length <= 65536 ? msg.text : '';
      } else if (state.phase === 'running' && msg.type === 'ask' && msg.ask === 'completion_result') {
        // Zoo presents this ask after the assistant's completion result.
        await endTurn(state);
      } else if (state.phase === 'idle' && event.action === 'created' && msg.type === 'say' && msg.say === 'user_feedback') {
        // This is feedback to the completion ask, not a tool approval.
        state.phase = 'running';
        const prompt = typeof msg.text === 'string' && msg.text.length <= 65536 ? msg.text : '';
        await send('turn_start', { ...state.payload, prompt });
      }
    }
    function listen(name, fn) {
      const listener = (...args) => enqueue(() => fn(...args));
      api.on(name, listener);
      listeners.push([name, listener]);
    }
    listen('taskStarted', start);
    listen('taskCompleted', finish);
    listen('taskAborted', finish);
    const onMessage = (event) => {
      const msg = event?.message;
      if (msg && ((msg.type === 'ask' && msg.ask === 'completion_result') ||
          (msg.type === 'say' && (msg.say === 'completion_result' || msg.say === 'user_feedback')))) {
        enqueue(() => message(event));
      }
    };
    api.on('message', onMessage);
    listeners.push(['message', onMessage]);
    disposeObserver = () => {
      disposed = true;
      for (const [name, listener] of listeners) api.removeListener(name, listener);
    };
  }).catch(() => {});
}

function deactivate() { stopped = true; disposeObserver(); }
module.exports = { activate, deactivate };
