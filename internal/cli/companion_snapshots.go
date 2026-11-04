package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

// Only an explicitly approved personal connection publishes derived context.
// Manual notes and companion database files remain local.
func saveCompanionSnapshot(ctx context.Context, e *engine.Engine) error {
	data, err := os.ReadFile(loginPath(e.Root))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg syncclient.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Mode != "personal" {
		return nil
	}
	var step int64
	if err := e.DB.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM keyframes").Scan(&step); err != nil {
		return err
	}
	brain, graph := companions.Lookup(ctx, e.Root, "", "", 20)
	memories := make([]companions.Memory, 0, len(brain.Memories))
	for _, memory := range brain.Memories {
		if memory.Source == "trace-import" || memory.Source == "agent-extract" {
			memories = append(memories, memory)
		}
	}
	brain.Memories = memories
	graph.Repo = cfg.Remote
	payload, err := json.Marshal(struct {
		Schema     int                      `json:"schema"`
		SourceStep int64                    `json:"source_step"`
		Brain      companions.MemoryContext `json:"brain"`
		Graph      companions.CodeContext   `json:"graph"`
	}{1, step, brain, graph})
	if err != nil {
		return err
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return err
	}
	var cleanValue func(any) any
	cleanValue = func(value any) any {
		switch v := value.(type) {
		case string:
			return strings.ReplaceAll(e.Redactor.Text(v), e.Root, "[repository]")
		case []any:
			for i := range v {
				v[i] = cleanValue(v[i])
			}
		case map[string]any:
			for key, item := range v {
				v[key] = cleanValue(item)
			}
		}
		return value
	}
	encoded, err := json.Marshal(cleanValue(document))
	if err != nil {
		return err
	}
	clean := string(encoded)

	sum := sha256.Sum256([]byte(clean))
	snapshotID, err := id.New()
	if err != nil {
		return err
	}
	if _, err := e.DB.ExecContext(ctx, "INSERT OR IGNORE INTO context_snapshots(id,source_step,created_at,payload,fingerprint) VALUES(?,?,?,?,?)", snapshotID, step, time.Now().UnixMilli(), clean, hex.EncodeToString(sum[:])); err != nil {
		return fmt.Errorf("saving companion snapshot: %w", err)
	}
	return nil
}
