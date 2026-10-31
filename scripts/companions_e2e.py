#!/usr/bin/env python3
"""Exercise the actual CLI, Brain and Graph binaries in isolated checkouts."""
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile
import time


SOURCE = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="trace-companions-") as temporary:
        temporary = Path(temporary).resolve()
        binaries = temporary / "bin"
        binaries.mkdir()
        for name in ("trace-cli", "trace-brain", "trace-graph"):
            repo = SOURCE if name == "trace-cli" else SOURCE.parent / name
            binary = "trace" if name == "trace-cli" else name
            subprocess.run(
                ["go", "build", "-buildvcs=false", "-o", str(binaries / binary), "./cmd/" + binary],
                cwd=repo, check=True, env=dict(os.environ, CGO_ENABLED="0"),
            )
        environment = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"], TRACE_COMPANIONS="1")
        trace = str(binaries / "trace")
        project = temporary / "project"
        recovered = temporary / "recovered"
        daemon_roots = []

        def run(root, *args, data=None, json_output=False, cwd=None):
            command = [trace, "--repo", str(root)]
            if json_output:
                command.append("--json")
            result = subprocess.run(command + list(args), cwd=cwd or temporary, env=environment,
                                    input=data, text=True, capture_output=True, timeout=30)
            if result.returncode:
                raise AssertionError(f"{args}: {result.stderr}")
            return json.loads(result.stdout) if json_output else result.stdout

        def wait_for(predicate, description):
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                value = predicate()
                if value:
                    return value
                time.sleep(0.05)
            raise AssertionError("timed out: " + description)

        def initialize(root, content):
            root.mkdir()
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "main.go").write_text(content)

        def hook(event, **fields):
            fields.update(session_id="companion-e2e", hook_event_name=event)
            run(project, "hook", "claude-code", event, data=json.dumps(fields) + "\n")

        try:
            initialize(project, "package p\nfunc Validate() bool {return true}\n")
            # Register cleanup before any command that could launch a daemon.
            daemon_roots.append(project)
            run(project, "init")
            run(project, "daemon", "start")
            run(project, "brain", "remember", "authentication manual note ghp_abcdefghijklmnopqrstuvwxyz")
            hook("SessionStart")
            hook("UserPromptSubmit", prompt="authentication Login change")
            (project / "main.go").write_text("package p\nfunc Validate() bool {return true}\nfunc Login() bool {return Validate()}\n")
            hook("PostToolUse", tool_name="Write", tool_input={"file_path": str(project / "main.go")})
            hook("Stop", last_assistant_message="authentication Login finished")

            def complete_context():
                out = run(project, "context", "authentication", "--path", "main.go", json_output=True)
                return out if (out["steps"] and any(m.get("turn_id") for m in out["brain"]["memories"])
                               and any(s["name"] == "Login" for s in out["graph"]["symbols"])) else None

            context = wait_for(complete_context, "automatic post-turn import and indexing")
            assert context["brain"]["status"] == context["graph"]["status"] == "ready", context
            assert any(r["from_symbol"] == "Login" and r["to_symbol"] == "Validate" for r in context["graph"]["relations"]), context
            assert "ghp_abcdefghijklmnopqrstuvwxyz" not in json.dumps(context), "context leaked manual memory token"
            assert (project / ".trace/brain/memory.db").exists(), "Brain wrote outside integrated state"
            assert (project / ".trace/graph/graph.db").exists(), "Graph wrote outside integrated state"
            run(project, "companions", "disable-auto")
            assert not run(project, "companions", "status", json_output=True)["automatic"]
            run(project, "companions", "enable-auto")
            assert run(project, "companions", "status", json_output=True)["automatic"]

            # Rebuild the same recovered history in a different checkout. The
            # existing sync protocol is tested separately; this isolates the
            # post-recovery rebuild and proves manual memories stay local.
            initialize(recovered, (project / "main.go").read_text())
            (recovered / ".trace").mkdir()
            source = sqlite3.connect((project / ".trace/trace.db").as_uri() + "?mode=ro", uri=True)
            target = sqlite3.connect(recovered / ".trace/trace.db")
            try:
                source.backup(target)
            finally:
                source.close()
                target.close()
            shutil.copytree(project / ".trace/objects", recovered / ".trace/objects")
            report = run(recovered, "companions", "refresh", json_output=True)
            assert report["brain"]["status"] == report["graph"]["status"] == "ready", report
            recovery = run(recovered, "context", "authentication", "--path", "main.go", json_output=True)
            assert recovery["steps"] and any(m.get("turn_id") for m in recovery["brain"]["memories"]), recovery
            assert recovery["graph"]["repo"] == str(recovered), recovery
            assert not any(m["source"] == "manual" for m in recovery["brain"]["memories"]), "manual memories were transferred"
            # An unchanged import must preserve manual memory in the source.
            run(project, "companions", "refresh", json_output=True)
            source_context = run(project, "context", "authentication", json_output=True)
            assert any(m["source"] == "manual" for m in source_context["brain"]["memories"]), source_context
            print("companions e2e passed: automatic refresh, shared context, redaction, isolated project state and derived recovery")
        finally:
            for root in daemon_roots:
                run(root, "daemon", "stop")
                wait_for(lambda: not (root / ".trace/daemon.pid").exists(), "daemon shutdown")


if __name__ == "__main__":
    main()
