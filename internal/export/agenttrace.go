package export

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/store"
)

type Contributor struct {
	Type string `json:"type"`
}
type Range struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}
type Conversation struct {
	URL         string      `json:"url,omitempty"`
	Contributor Contributor `json:"contributor"`
	Ranges      []Range     `json:"ranges"`
}
type File struct {
	Path          string         `json:"path"`
	Conversations []Conversation `json:"conversations"`
}
type VCS struct {
	Type     string `json:"type"`
	Revision string `json:"revision"`
}
type Tool struct {
	Name string `json:"name"`
}
type Record struct {
	Version   string         `json:"version"`
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	VCS       *VCS           `json:"vcs,omitempty"`
	Tool      Tool           `json:"tool"`
	Files     []File         `json:"files"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func uuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("creating trace UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func AgentTrace(db *store.DB, w io.Writer) (int, error) {
	rows, err := db.Query(`SELECT o.path,o.start_line,o.end_line,o.at,COALESCE(o.turn_id,''),
		COALESCE(s.agent,''),COALESCE(k.seq,0),COALESCE(c.sha,''),o.kind,COALESCE(o.detail,'')
		FROM ops o LEFT JOIN turns t ON t.id=o.turn_id LEFT JOIN sessions s ON s.id=t.session_id
		LEFT JOIN keyframes k ON k.turn_id=t.id LEFT JOIN commits c ON c.keyframe_id=k.id
		WHERE o.path!='' AND o.start_line>0 AND o.end_line>=o.start_line
		AND (o.before_hash IS NOT NULL OR o.after_hash IS NOT NULL) ORDER BY o.at,o.id`)
	if err != nil {
		return 0, fmt.Errorf("querying operations: %w", err)
	}
	defer rows.Close()
	enc := json.NewEncoder(w)
	count := 0
	for rows.Next() {
		var path, turnID, agent, sha, opKind, detail string
		var start, end int
		var at, step int64
		if err := rows.Scan(&path, &start, &end, &at, &turnID, &agent, &step, &sha, &opKind, &detail); err != nil {
			return count, err
		}
		uid, err := uuid()
		if err != nil {
			return count, err
		}
		kind := "ai"
		if turnID == "" {
			kind = "human"
		}
		conversation := Conversation{Contributor: Contributor{Type: kind}, Ranges: []Range{{StartLine: start, EndLine: end}}}
		if turnID != "" {
			conversation.URL = "trace://turn/" + turnID
		}
		toolName := agent
		if toolName == "" {
			toolName = "trace"
		}
		metadata := map[string]any{"step": step, "agent": agent, "kind": opKind}
		if detail != "" {
			metadata["symbol"] = detail
		}
		r := Record{Version: "0.1.0", ID: uid, Timestamp: time.UnixMilli(at).UTC().Format(time.RFC3339), Tool: Tool{Name: toolName}, Files: []File{{Path: path, Conversations: []Conversation{conversation}}}, Metadata: map[string]any{"dev.usetrace": metadata}}
		if sha != "" {
			r.VCS = &VCS{Type: "git", Revision: sha}
		}
		if err := enc.Encode(r); err != nil {
			return count, fmt.Errorf("writing Agent Trace: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("reading operations: %w", err)
	}
	return count, nil
}
