# Trace LangGraph bridge. Managed by trace agent add/remove.
import json
import queue
import subprocess
import threading

from langchain_core.callbacks import BaseCallbackHandler

TRACE = __TRACE_BIN__
ROOT = __TRACE_ROOT__


class TraceCallbackHandler(BaseCallbackHandler):
    """Record root graph invocations; pass an instance in config['callbacks']."""

    def __init__(self):
        super().__init__()
        self._runs = set()
        self._lock = threading.Lock()
        self._queue = queue.SimpleQueue()
        threading.Thread(target=self._worker, daemon=True).start()

    def _worker(self):
        while True:
            kind, payload = self._queue.get()
            try:
                subprocess.run(
                    [TRACE, "--repo", ROOT, "hook", "langgraph", kind],
                    input=json.dumps(payload).encode(),
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                    timeout=2, check=False,
                )
            except (OSError, subprocess.SubprocessError):
                pass

    def _send(self, kind, run_id, **fields):
        self._queue.put((kind, {"session_id": str(run_id), **fields}))

    def on_chain_start(self, serialized, inputs, *, run_id, parent_run_id=None, **kwargs):
        if parent_run_id is not None:
            return
        with self._lock:
            self._runs.add(run_id)
        prompt = inputs if isinstance(inputs, str) else ""
        if isinstance(inputs, dict):
            value = inputs.get("input", inputs.get("prompt", ""))
            prompt = value if isinstance(value, str) else ""
        self._send("session_start", run_id)
        self._send("turn_start", run_id, prompt=prompt)

    def _finish(self, run_id, parent_run_id):
        if parent_run_id is not None:
            return
        with self._lock:
            if run_id not in self._runs:
                return
            self._runs.remove(run_id)
        self._send("turn_end", run_id)
        self._send("session_end", run_id)

    def on_chain_end(self, outputs, *, run_id, parent_run_id=None, **kwargs):
        self._finish(run_id, parent_run_id)

    def on_chain_error(self, error, *, run_id, parent_run_id=None, **kwargs):
        self._finish(run_id, parent_run_id)
