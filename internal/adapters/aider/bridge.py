# Trace Aider bridge. Managed by trace agent add/remove.
import json
import queue
import subprocess
import threading
import time
import uuid

TRACE = __TRACE_BIN__
ROOT = __TRACE_ROOT__


class TraceAider:
    """Wrap explicit Coder.run(message) calls; interactive chat is not covered."""

    def __init__(self, coder):
        self.coder = coder
        self.session_id = str(uuid.uuid4())
        self._events = queue.Queue(maxsize=256)
        self._run_lock = threading.Lock()
        threading.Thread(target=self._worker, daemon=True, name="trace-aider").start()

    def _worker(self):
        while True:
            batch = self._events.get()
            try:
                for name, payload in batch:
                    try:
                        subprocess.run([TRACE, "--repo", ROOT, "hook", "aider", name],
                                       input=json.dumps(payload), text=True, timeout=2,
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                    except (OSError, subprocess.SubprocessError):
                        pass
            finally:
                self._events.task_done()

    def _send(self, batch):
        try:
            self._events.put_nowait(batch)
            return True
        except queue.Full:
            return False

    def run(self, *args, **kwargs):
        # Keep the bridge's original message= spelling as an alias while also
        # accepting Aider's native with_message= calling convention.
        if not args and "message" in kwargs and "with_message" not in kwargs:
            kwargs = dict(kwargs)
            args = (kwargs.pop("message"),)
        message = args[0] if args else kwargs.get("with_message")
        if not isinstance(message, str) or not message:
            return self.coder.run(*args, **kwargs)
        stripped = message.lstrip()
        preproc = args[1] if len(args) > 1 else kwargs.get("preproc", True)
        if preproc and stripped.startswith(("/", "!")):
            parts = stripped.split(None, 1)
            if len(parts) != 2 or parts[0] not in ("/ask", "/code", "/architect", "/context") or not parts[1].strip():
                return self.coder.run(*args, **kwargs)
        # A session has only one active Trace turn. Preserve concurrent coder
        # calls without creating a competing turn; overlapping edits may still
        # be included in the first invocation's repository snapshot.
        if not self._run_lock.acquire(blocking=False):
            return self.coder.run(*args, **kwargs)
        try:
            payload = {"session_id": self.session_id}
            prompt = message if len(message) <= 65536 else ""
            # Reserve queue capacity for the matching end batch. Only the
            # holder of _run_lock may add lifecycle events.
            recorded = self._events.qsize() <= self._events.maxsize - 2 and self._send(
                [("turn_start", {**payload, "prompt": prompt})]
            )
            response = None
            try:
                response = self.coder.run(*args, **kwargs)
                return response
            finally:
                if recorded:
                    # Aider can change files without reporting each path.
                    batch = [("tool_edit", payload)]
                    if isinstance(response, str) and response and len(response) <= 65536:
                        batch.append(("turn_context", {**payload, "context_type": "response", "text": response}))
                    batch.append(("turn_end", payload))
                    self._send(batch)
        finally:
            self._run_lock.release()

    def finish(self):
        if self._run_lock.acquire(blocking=False):
            try:
                self._send([("session_end", {"session_id": self.session_id})])
            finally:
                self._run_lock.release()

    def flush(self, timeout=5):
        """Wait up to timeout seconds for queued events; return whether drained."""
        deadline = time.monotonic() + max(0, timeout)
        with self._events.all_tasks_done:
            while self._events.unfinished_tasks:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                self._events.all_tasks_done.wait(remaining)
        return True
