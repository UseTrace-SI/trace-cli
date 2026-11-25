package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestGCCollectsEmptySessions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		dryRun       bool
		wantSessions int
	}{
		{"dry run", true, 1},
		{"collect", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			ev := event.Event{Agent: "codex", AgentSessionID: "s", Worktree: root, At: time.Now().Add(-time.Hour).UnixMilli(), Kind: event.TurnStart}
			if err := e.Process(ev); err != nil {
				t.Fatal(err)
			}
			ev.Kind, ev.At = event.TurnEnd, time.Now().Add(-time.Hour).UnixMilli()+1
			if err := e.Process(ev); err != nil {
				t.Fatal(err)
			}
			var sessionID string
			if err := e.DB.QueryRow("SELECT session_id FROM turns").Scan(&sessionID); err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []string{
				"INSERT INTO sync_updates(id,table_name,row_id,data) VALUES('u','sessions','" + sessionID + "','{}')",
				"INSERT INTO transcript_offsets(session_id,path,offset) VALUES('" + sessionID + "','p',1)",
				"UPDATE keyframes SET created_at=1 WHERE tag='turn'",
			} {
				if _, err := e.DB.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			result, err := e.GC(0, tc.dryRun)
			if err != nil {
				t.Fatal(err)
			}
			var sessions int
			if err := e.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if sessions != tc.wantSessions {
				t.Fatalf("sessions = %d, want %d", sessions, tc.wantSessions)
			}
			if tc.dryRun {
				return
			}
			if result.RemovedSessions != 1 {
				t.Fatalf("removed sessions = %d, want 1", result.RemovedSessions)
			}
			for _, query := range []string{
				"SELECT COUNT(*) FROM sync_updates WHERE table_name='sessions'",
				"SELECT COUNT(*) FROM transcript_offsets",
			} {
				var n int
				if err := e.DB.QueryRow(query).Scan(&n); err != nil || n != 0 {
					t.Fatalf("%s = %d, %v", query, n, err)
				}
			}
		})
	}
}

// An unattended collection keeps unreferenced objects that are still fresh:
// another process may be storing them for a keyframe it has not recorded.
func TestGCMinBlobAgeKeepsFreshUnreferencedObjects(t *testing.T) {
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
	fresh, err := e.Blobs.PutBytes([]byte("written by a concurrent rewind\n"))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := e.Blobs.PutBytes([]byte("left by a collected keyframe\n"))
	if err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(root, ".trace", "objects", string(stale)[:2], string(stale)[2:])
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GCWith(GCOptions{KeepDays: 30, MinBlobAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Blobs.Get(fresh); err != nil {
		t.Fatalf("fresh unreferenced object removed: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale unreferenced object kept: %v", err)
	}
	// Without a grace period (trace gc), both are collected as before.
	if _, err := e.GC(30, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Blobs.Get(fresh); err == nil {
		t.Fatal("trace gc kept an unreferenced object")
	}
}
