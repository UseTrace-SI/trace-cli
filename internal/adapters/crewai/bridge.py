# Trace CrewAI bridge. Managed by trace agent add/remove.
import json
import queue
import subprocess
import threading

from crewai.events import BaseEventListener, CrewKickoffStartedEvent, CrewKickoffCompletedEvent, CrewKickoffFailedEvent

TRACE = __TRACE_BIN__
ROOT = __TRACE_ROOT__


class TraceCrewListener(BaseEventListener):
    """Record correlated crew kickoffs; instantiate once before kickoff."""

    def __init__(self):
        self._runs = set()
        self._lock = threading.Lock()
        self._events = queue.Queue(maxsize=256)
        threading.Thread(target=self._worker, daemon=True, name="trace-crewai").start()
        super().__init__()

    def _worker(self):
        while True:
            batch = self._events.get()
            try:
                for kind, payload in batch:
                    try:
                        subprocess.run([TRACE, "--repo", ROOT, "hook", "crewai", kind],
                                       input=json.dumps(payload), text=True, timeout=2,
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                    except (OSError, subprocess.SubprocessError):
                        pass
            finally:
                self._events.task_done()

    def _start(self, event):
        run_id = getattr(event, "event_id", None)
        if not isinstance(run_id, str) or not run_id:
            return
        inputs = getattr(event, "inputs", None)
        prompt = ""
        if isinstance(inputs, dict):
            value = inputs.get("prompt", inputs.get("input", ""))
            if isinstance(value, str) and len(value) <= 65536:
                prompt = value
        batch = [("session_start", {"session_id": run_id}),
                 ("turn_start", {"session_id": run_id, "prompt": prompt})]
        with self._lock:
            if run_id in self._runs:
                return
            try:
                self._events.put_nowait(batch)
            except queue.Full:
                return
            self._runs.add(run_id)

    def _finish(self, event):
        run_id = getattr(event, "started_event_id", None)
        if not isinstance(run_id, str) or not run_id:
            return
        with self._lock:
            if run_id not in self._runs:
                return
            try:
                # A crew may edit files through arbitrary tools. Signal an
                # unknown side effect so Trace can scan when the watcher has
                # not observed changes before the completion event arrives.
                self._events.put_nowait([("tool_edit", {"session_id": run_id}),
                                         ("turn_end", {"session_id": run_id}),
                                         ("session_end", {"session_id": run_id})])
            except queue.Full:
                return
            self._runs.remove(run_id)

    def setup_listeners(self, bus):
        @bus.on(CrewKickoffStartedEvent)
        def start(source, event):
            self._start(event)

        @bus.on(CrewKickoffCompletedEvent)
        def finish(source, event):
            self._finish(event)

        @bus.on(CrewKickoffFailedEvent)
        def failed(source, event):
            self._finish(event)
