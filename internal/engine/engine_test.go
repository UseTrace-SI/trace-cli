package engine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestTurnsAndUndoableRewind(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	p := filepath.Join(root, "file.txt")
	if err := os.WriteFile(p, []byte("start\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	base := event.Event{Agent: "claude-code", AgentSessionID: "session-a", Worktree: root, At: time.Now().UnixMilli()}
	base.Kind = event.SessionStart
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	for i, value := range []string{"one\n", "two\n", "three\n"} {
		base.Kind = event.TurnStart
		base.Prompt = "turn"
		if err := e.Process(base); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		base.Kind = event.ToolEdit
		base.Paths = []string{p}
		if err := e.Process(base); err != nil {
			t.Fatal(err)
		}
		base.Kind = event.TurnEnd
		if err := e.Process(base); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE tag='turn'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != i+1 {
			t.Fatalf("turn keyframes: %d", n)
		}
	}
	base.Kind = event.SessionEnd
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	var updates int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM sync_updates WHERE table_name='turns'").Scan(&updates); err != nil || updates != 3 {
		t.Fatalf("completed turn updates: %d, %v", updates, err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM sync_updates WHERE table_name='sessions'").Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("session end updates: %d, %v", updates, err)
	}
	safety, changed, err := e.Rewind(2, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if safety != 4 || len(changed) != 1 || changed[0] != "file.txt" {
		t.Fatalf("safety=%d changed=%v", safety, changed)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "two\n" {
		t.Fatalf("rewind: %q", b)
	}
	before, err := e.ManifestAt(3)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.ManifestAt(2)
	if err != nil {
		t.Fatal(err)
	}
	var beforeHash, afterHash string
	if err := e.DB.QueryRow("SELECT before_hash, after_hash FROM ops WHERE path='file.txt' ORDER BY rowid DESC LIMIT 1").Scan(&beforeHash, &afterHash); err != nil {
		t.Fatal(err)
	}
	if beforeHash != string(before["file.txt"].Hash) || afterHash != string(after["file.txt"].Hash) {
		t.Fatalf("rewind operation hashes: %q -> %q", beforeHash, afterHash)
	}
	if _, _, err := e.Rewind(safety, "", false); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "three\n" {
		t.Fatalf("undo: %q", b)
	}
}

func TestGCProtectsBaselineAndCurrentTree(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	p := filepath.Join(root, "file.txt")
	if err := os.WriteFile(p, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "s", Worktree: root, At: time.Now().UnixMilli()}
	ev.Kind = event.TurnStart
	ev.Prompt = "old turn"
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE keyframes SET created_at=1 WHERE seq=1"); err != nil {
		t.Fatal(err)
	}
	result, err := e.GC(30, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedKeyframes != 1 {
		t.Fatalf("dry run: %+v", result)
	}
	result, err = e.GC(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedKeyframes != 1 {
		t.Fatalf("gc: %+v", result)
	}
	if _, err := e.ManifestAt(0); err != nil {
		t.Fatalf("baseline removed: %v", err)
	}
	if _, err := e.ManifestAt(1); err == nil {
		t.Fatal("old keyframe retained")
	}
	if _, err := e.ManifestAt(2); err != nil {
		t.Fatalf("safety keyframe missing: %v", err)
	}
	var obsolete int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM sync_updates WHERE table_name='turns'").Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete turn update survived garbage collection: %d, %v", obsolete, err)
	}
}

func TestTurnEndAttributesWatcherEditQueuedBeforeTurnStart(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-time.Second).UnixMilli()
	if err := os.WriteFile(path, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordChanges([]string{path}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id IS NULL AND source='watcher'").Scan(&before); err != nil || before == 0 {
		t.Fatalf("initial attribution: %d, %v", before, err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "late", Worktree: root, At: startedAt, Kind: event.TurnStart}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	ev.At = time.Now().Add(time.Second).UnixMilli()
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var attributed int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id IS NOT NULL AND source='watcher'").Scan(&attributed); err != nil || attributed == 0 {
		t.Fatalf("final attribution: %d, %v", attributed, err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM sync_updates WHERE table_name='ops'").Scan(&attributed); err != nil || attributed == 0 {
		t.Fatalf("synced watcher attribution: %d, %v", attributed, err)
	}
}

func TestBashWritesUseWatcherPathsWithoutFullScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not block root from scanning")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "claude-code", AgentSessionID: "bash-writes", Worktree: root, At: time.Now().UnixMilli(), Kind: event.TurnStart}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.ToolName = event.ToolEdit, "Bash"
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if err := e.RecordChanges(paths); err != nil {
		t.Fatal(err)
	}
	// A full scan would try to read this unrelated file and fail. The watcher
	// has already supplied every path written by the Bash command.
	blocked := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(blocked, []byte("not part of the command\n"), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0600)
	ev.Kind, ev.ToolName = event.TurnEnd, ""
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	m, err := e.ManifestAt(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if _, ok := m[name]; !ok {
			t.Errorf("watcher path %s missing from keyframe", name)
		}
	}
}

func TestEveryFiftiethKeyframeRescansMissedChanges(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "periodic", Worktree: root, At: time.Now().UnixMilli()}
	for step := 1; step <= 50; step++ {
		ev.Kind = event.TurnStart
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		if step == 50 {
			if err := os.WriteFile(path, []byte("missed watcher\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ev.Kind = event.TurnEnd
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	m, err := e.ManifestAt(50)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Blobs.Get(m["file.txt"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "missed watcher\n" {
		t.Fatalf("periodic scan missed change: %q", b)
	}
}
