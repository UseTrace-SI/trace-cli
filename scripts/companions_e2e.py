#!/usr/bin/env python3
"""Exercise the actual CLI, Brain and Graph binaries in isolated checkouts."""
import base64
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
        environment = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"], TRACE_COMPANIONS="1", TRACE_HOME=str(temporary / "trace-user"))
        trace = str(binaries / "trace")
        project = temporary / "project"
        recovered = temporary / "recovered"
        daemons = []

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
            (project / ".trace").mkdir()
            (project / ".trace/login.json").write_text(json.dumps({
                "endpoint": "https://trace.example", "remote": "solo/project", "mode": "personal", "auto_sync": False,
            }))
            # Own the test process handle so cleanup does not need process-list
            # permissions or trust a PID that another process could recycle.
            process = subprocess.Popen([trace, "--repo", str(project), "daemon"], env=environment,
                                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            daemons.append(process)
            wait_for(lambda: (project / ".trace/spool.ready").exists() or (project / ".trace/sock").exists(), "daemon readiness")
            run(project, "init")
            run(project, "daemon", "start")
            manual_id = run(project, "brain", "remember", "authentication manual note ghp_abcdefghijklmnopqrstuvwxyz").strip().split()[-1]
            private_docs = temporary / "private-docs"
            private_docs.mkdir()
            document = private_docs / "guide.md"
            document.write_text("authentication localdocumentkeyword ghp_abcdefghijklmnopqrstuvwxyz")
            document_sync = json.loads(run(project, "brain", "documents", "sync", "--root", str(private_docs)))
            assert document_sync["result"]["imported"] == 1 and document_sync["local_only"], document_sync
            document_recall = run(project, "brain", "recall", "localdocumentkeyword")
            assert "localdocumentkeyword" in document_recall and "ghp_abcdefghijklmnopqrstuvwxyz" not in document_recall
            run(project, "brain", "update", manual_id, "authentication revised note ghp_abcdefghijklmnopqrstuvwxyz")
            manual = json.loads(run(project, "brain", "get", manual_id))
            assert "revised note" in manual["content"] and "ghp_abcdefghijklmnopqrstuvwxyz" not in manual["content"], manual
            versions = json.loads(run(project, "brain", "history", manual_id))
            fact = json.loads(run(project, "brain", "facts", "assert", "project", "uses", "sqlite", "--evidence", manual_id,
                                  "--from", "2024-01-01T00:00:00Z", "--exclusive"))
            assert fact["source_verified"] is False and fact["local_only"], fact
            claims = json.loads(run(project, "brain", "facts", "query", "--subject", "project", "--at", "2024-02-01T00:00:00Z"))
            assert len(claims["result"]["facts"]) == 1 and claims["result"]["facts"][0]["object"] == "sqlite", claims
            fact_graph = json.loads(run(project, "brain", "facts", "graph", "project"))
            assert any(item["id"] == fact["fact"]["id"] for item in fact_graph["result"]["facts"]), fact_graph
            assert len(versions["revisions"]) == 2, versions
            verified = json.loads(run(project, "brain", "verify", manual_id))
            assert verified["status"] == "unlinked" and verified["content_verified"] is False, verified
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
            # An explicit personal connection publishes bounded derived context,
            # while manual memories remain private to this machine.
            def personal_snapshot():
                with sqlite3.connect(project / ".trace/trace.db") as db:
                    row = db.execute("SELECT payload FROM context_snapshots ORDER BY id DESC LIMIT 1").fetchone()
                    if not row:
                        return None
                    value = json.loads(row[0])
                    return value if any(m.get("turn_id") for m in value["brain"]["memories"]) else None
            snapshot = wait_for(personal_snapshot, "Brain and Graph snapshot for personal dashboard")
            assert snapshot["brain"]["status"] == snapshot["graph"]["status"] == "ready", snapshot
            assert snapshot["graph"]["repo"] == "solo/project", snapshot
            assert snapshot["brain"]["memories"], snapshot
            assert all(m["source"] in ("trace-import", "agent-extract") for m in snapshot["brain"]["memories"]), snapshot
            assert "localdocumentkeyword" not in json.dumps(snapshot), "local document was uploaded"
            assert str(project) not in json.dumps(snapshot), "snapshot exposed the local repository path"
            assert context["brain"]["status"] == context["graph"]["status"] == "ready", context
            assert any(r["from_symbol"] == "Login" and r["to_symbol"] == "Validate" for r in context["graph"]["relations"]), context
            assert "ghp_abcdefghijklmnopqrstuvwxyz" not in json.dumps(context), "context leaked manual memory token"
            assert (project / ".trace/brain/memory.db").exists(), "Brain wrote outside integrated state"
            assert (project / ".trace/graph/graph.db").exists(), "Graph wrote outside integrated state"
            # Exercise the new graph API through the parent CLI's real plugin
            # dispatch, including complete exports and a later symbol change.
            graph_health = json.loads(run(project, "graph", "health"))
            assert graph_health["resolved_calls"] >= 1, graph_health
            graph_base = temporary / "graph-before.ndjson"
            graph_base.write_text(run(project, "graph", "snapshot"))
            exported = [json.loads(line) for line in graph_base.read_text().splitlines()]
            assert exported[0]["format"] == "trace.graph/v1" and exported[-1]["type"] == "complete", exported
            assert exported[-1]["truncated"] is False, exported
            with (project / "main.go").open("a") as changed:
                changed.write("func Controller() bool {return Login()}\n")
                changed.write("type Worker struct{}\ntype WorkerAlias = Worker\nfunc (Worker) Execute(){}\nfunc Dispatch(){instance:=WorkerAlias{};instance.Execute()}\n")
            run(project, "companions", "refresh", json_output=True)
            impact = json.loads(run(project, "graph", "impact", "Validate", "--json"))
            assert {node["name"] for node in impact["nodes"]} == {"Login", "Controller"}, impact
            receiver_impact = json.loads(run(project, "graph", "impact", "Execute", "--json"))
            assert {node["name"] for node in receiver_impact["nodes"]} == {"Dispatch"}, receiver_impact
            changes = json.loads(run(project, "graph", "changes", "--base", str(graph_base)))
            assert any(item["name"] == "Controller" and item["kind"] == "added" for item in changes["changes"]), changes
            # Pausing also affects an already-running daemon, without deleting
            # the project history or requiring hooks to be uninstalled.
            run(project, "disable")
            with sqlite3.connect(project / ".trace/trace.db") as db:
                before_pause = db.execute("SELECT count(*) FROM turns").fetchone()[0]
                before_ops = db.execute("SELECT count(*) FROM ops").fetchone()[0]
            (project / "paused-note.txt").write_text("edited while paused\n")
            time.sleep(0.5)
            hook("UserPromptSubmit", prompt="must-not-record-while-paused")
            hook("Stop")
            with sqlite3.connect(project / ".trace/trace.db") as db:
                # Earlier, already admitted watcher work can finish after the
                # marker is written. Test the paused mutations themselves and
                # preserve the existing history rather than racing row totals.
                assert db.execute("SELECT count(*) FROM turns").fetchone()[0] >= before_pause
                assert db.execute("SELECT count(*) FROM ops").fetchone()[0] >= before_ops
                assert db.execute("SELECT count(*) FROM turns WHERE prompt='must-not-record-while-paused'").fetchone()[0] == 0
                assert db.execute("SELECT count(*) FROM ops WHERE path='paused-note.txt'").fetchone()[0] == 0, "file watcher recorded while paused"
            setup_home = temporary / "enable-home"
            setup_home.mkdir()
            subprocess.run([trace, "enable", "--agent", "claude-code"],
                           env=dict(environment, HOME=str(setup_home), CLAUDE_CONFIG_DIR=str(setup_home / "claude")),
                           check=True, capture_output=True, text=True, timeout=30)
            hook("UserPromptSubmit", prompt="authentication resumed capture")
            hook("Stop")
            def resumed():
                with sqlite3.connect(project / ".trace/trace.db") as db:
                    return db.execute("SELECT count(*) FROM turns WHERE prompt='authentication resumed capture' AND ended_at IS NOT NULL").fetchone()[0] == 1
            wait_for(resumed, "recording resumed without restarting the daemon")
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
            recovered_claims = json.loads(run(recovered, "brain", "facts", "query", "--subject", "project"))
            assert recovered_claims["result"]["facts"] == [], "local fact assertions were transferred"
            assert recovery["steps"] and any(m.get("turn_id") for m in recovery["brain"]["memories"]), recovery
            assert recovery["graph"]["repo"] == str(recovered), recovery
            assert not any(m["source"] == "manual" for m in recovery["brain"]["memories"]), "manual memories were transferred"
            # An unchanged import must preserve manual memory in the source.
            run(project, "companions", "refresh", json_output=True)
            source_context = run(project, "context", "authentication", json_output=True)
            assert any(m["source"] == "manual" for m in source_context["brain"]["memories"]), source_context
            workspace = json.loads(run(project, "brain", "workspace", "query", "authentication", "--databases",
                                       str(project / ".trace/brain/memory.db"), "--databases", str(recovered / ".trace/brain/memory.db")))
            assert workspace["partial"] is False and len(workspace["sources"]) == 2, workspace
            run(project, "brain", "forget", manual_id)
            assert json.loads(run(project, "brain", "facts", "query", "--subject", "project"))["result"]["facts"] == [], "forgotten evidence left fact content"
            document.write_text("freshdocumentkeyword")
            refreshed = json.loads(run(project, "brain", "documents", "sync", "--root", str(private_docs)))
            assert refreshed["result"]["imported"] == 1, refreshed
            assert "localdocumentkeyword" not in run(project, "brain", "recall", "localdocumentkeyword")
            assert "freshdocumentkeyword" in run(project, "brain", "recall", "freshdocumentkeyword")
            document.unlink()
            pruned = json.loads(run(project, "brain", "documents", "sync", "--root", str(private_docs), "--prune"))
            assert pruned["result"]["pruned"] == 1, pruned
            # Exercise the shipped CLI's offline trust/install/run/revoke flow.
            # The fixture key is ephemeral; no existing credential is accessed.
            plugin_bundle = temporary / "plugin-bundle"
            plugin_bundle.mkdir()
            generator = """import {generateKeyPairSync,sign,createHash} from 'node:crypto';
import {writeFileSync} from 'node:fs';import {join} from 'node:path';
const path=process.argv[1],keys=generateKeyPairSync('ed25519');
const binary=Buffer.from(process.argv[2],'base64');
const manifest={format:1,name:'example',publisher:'fixture',version:'1.0.0',os:process.platform,arch:process.arch==='x64'?'amd64':process.arch,sha256:createHash('sha256').update(binary).digest('hex'),bytes:binary.length};
const raw=Buffer.from(JSON.stringify(manifest));
writeFileSync(join(path,'publisher.pem'),keys.publicKey.export({type:'spki',format:'pem'}));
writeFileSync(join(path,'manifest.json'),raw);writeFileSync(join(path,'manifest.sig'),sign(null,raw,keys.privateKey).toString('base64'));writeFileSync(join(path,'plugin'),binary);
"""
            plugin_code = base64.b64encode(b'#!/bin/sh\nprintf \'%s\\n\' "$@"\n').decode()
            subprocess.run(["node", "--input-type=module", "-e", generator, str(plugin_bundle), plugin_code], check=True, cwd=temporary, env=environment, timeout=15)
            trusted = run(project, "plugin", "trust", "--publisher", "fixture", "--key", str(plugin_bundle / "publisher.pem"), json_output=True)
            assert trusted["trusted"], trusted
            installed = run(project, "plugin", "install", str(plugin_bundle), json_output=True)
            assert installed["verified"] and installed["executed"] is False and installed["network"] is False, installed
            assert run(project, "plugin", "run", "example", "--", "$(literal)", "--repo=literal") == "$(literal)\n--repo=literal\n"
            run(project, "plugin", "untrust", "fixture", json_output=True)
            verified_plugins = run(project, "plugin", "list", json_output=True)
            assert len(verified_plugins) == 1 and verified_plugins[0]["verified"] is False, verified_plugins
            run(project, "plugin", "remove", "example", json_output=True)
            run(project, "plugin", "prune", "example", json_output=True)
            with sqlite3.connect(project / ".trace/brain/memory.db") as db:
                assert db.execute("SELECT count(*) FROM memories WHERE id=?", (manual_id,)).fetchone()[0] == 0
                assert db.execute("SELECT count(*) FROM memory_revisions WHERE memory_id=?", (manual_id,)).fetchone()[0] == 0
            print("companions e2e passed: automatic refresh, local documents, temporal facts, memory lifecycle, federated retrieval, graph impact/snapshots/changes, signed plugins, redaction, pause/resume and derived recovery")
        finally:
            for process in daemons:
                process.terminate()
                try:
                    process.communicate(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate(timeout=5)
                    raise AssertionError("test daemon did not shut down gracefully")


if __name__ == "__main__":
    main()
