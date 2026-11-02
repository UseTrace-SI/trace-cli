package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

func TestSyncStatusReportsBacklogAndConnectionHealth(t *testing.T) {
	root := competitiveRepo(t)
	cfg := syncclient.Config{Endpoint: "https://trace.example", Remote: "solo/project", Mode: "personal", ConnectionID: "alice", AutoSync: true}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(loginPath(root), b, 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.DB.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('pending','codex','repo',1)")
	e.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = syncclient.RecordAttempt(filepath.Join(root, ".trace"), cfg, errors.New("connection revoked")); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := syncStatusCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Pending int64             `json:"pending_rows"`
		Health  syncclient.Health `json:"health"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Pending < 1 || result.Health.LastError != "connection revoked" || result.Health.Action == "" {
		t.Fatalf("status: %+v", result)
	}
}
