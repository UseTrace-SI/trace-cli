package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestTurnEndRollsBackOpsAndKeyframeTogether(t *testing.T) {
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
	if err := os.WriteFile(path, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordChanges([]string{path}); err != nil {
		t.Fatal(err)
	}
	before, err := e.current.Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "atomic", Worktree: root, At: time.Now().Add(-time.Second).UnixMilli(), Kind: event.TurnStart}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	// Simulate a failure after the ops have been written but before the
	// keyframe insert completes.
	if _, err := e.DB.Exec("CREATE TRIGGER block_turn BEFORE INSERT ON keyframes WHEN NEW.tag='turn' BEGIN SELECT RAISE(ABORT, 'blocked'); END;"); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.At = event.TurnEnd, time.Now().UnixMilli()
	if err := e.Process(ev); err == nil {
		t.Fatal("turn end succeeded despite a failed keyframe insert")
	}
	var ops, keyframes int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id IS NOT NULL").Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE tag='turn'").Scan(&keyframes); err != nil {
		t.Fatal(err)
	}
	if ops != 0 || keyframes != 0 {
		t.Fatalf("failed turn left %d ops and %d keyframes", ops, keyframes)
	}
	after, err := e.current.Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("in-memory manifest changed for a rolled back turn")
	}
	if _, err := e.DB.Exec("DROP TRIGGER block_turn"); err != nil {
		t.Fatal(err)
	}
	if err := e.Process(ev); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id IS NOT NULL").Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE tag='turn'").Scan(&keyframes); err != nil {
		t.Fatal(err)
	}
	if ops == 0 || keyframes != 1 {
		t.Fatalf("retried turn has %d ops and %d keyframes", ops, keyframes)
	}
}
