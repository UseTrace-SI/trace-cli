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
      active.set(taskId, payload);
      const prompt = typeof item.task === 'string' && item.task.length <= 65536 ? item.task : '';
      await send('turn_start', { ...payload, prompt });
    }
    async function finish(taskId) {
      const payload = active.get(taskId);
      if (!payload) return;
      active.delete(taskId);
      // Task tools may have changed files without reporting paths.
      await send('tool_edit', payload);
      await send('turn_end', payload);
      await send('session_end', payload);
    }
    function listen(name, fn) {
      const listener = (...args) => enqueue(() => fn(...args));
      api.on(name, listener);
      listeners.push([name, listener]);
    }
    listen('taskStarted', start);
    listen('taskCompleted', finish);
    listen('taskAborted', finish);
    disposeObserver = () => {
      disposed = true;
      for (const [name, listener] of listeners) api.removeListener(name, listener);
    };
  }).catch(() => {});
}

function deactivate() { stopped = true; disposeObserver(); }
module.exports = { activate, deactivate };
