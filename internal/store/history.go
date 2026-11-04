package store

// HistorySchemaVersion versions the portable history contract independently of
// SQLite migrations and transport framing. Additive nullable fields allow old
// histories to be recovered without discarding fields from newer clients.
const HistorySchemaVersion = 1

var HistoryTables = []string{"sessions", "turns", "keyframes", "ops", "commits", "messages", "comments", "trails", "sync_updates", "context_snapshots"}

var HistoryColumns = map[string][]string{
	"sessions":          {"id", "agent", "agent_sid", "worktree", "started_at", "ended_at", "forked_from"},
	"turns":             {"id", "session_id", "seq", "prompt", "reasoning", "tool_calls", "files_read", "started_at", "ended_at", "tokens_in", "tokens_out", "cost_usd"},
	"keyframes":         {"id", "turn_id", "seq", "manifest_hash", "git_head", "tag", "created_at"},
	"ops":               {"id", "turn_id", "session_id", "path", "kind", "start_line", "end_line", "before_hash", "after_hash", "source", "at", "detail", "actor", "parent_op_id"},
	"commits":           {"sha", "keyframe_id", "at"},
	"messages":          {"id", "session_id", "turn_id", "role", "content", "at"},
	"comments":          {"id", "file", "line", "body", "turn_id", "session_id", "at"},
	"trails":            {"id", "title", "kind", "base_step", "head_step", "created_at"},
	"sync_updates":      {"id", "table_name", "row_id", "data"},
	"context_snapshots": {"id", "source_step", "created_at", "payload", "fingerprint"},
}
