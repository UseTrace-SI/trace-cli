package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

type Engine struct {
	mu       sync.Mutex
	current  manifest.Manifest
	Root     string
	DB       *store.DB
	Blobs    *blob.Store
	Redactor redact.Redactor
}

func Open(root string) (*Engine, error) {
	dir := repo.TraceDir(root)
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		return nil, err
	}
	bs, err := blob.Open(filepath.Join(dir, "objects"))
	if err != nil {
		db.Close()
		return nil, err
	}
	e := &Engine{Root: root, DB: db, Blobs: bs}
	var lastHash string
	err = db.QueryRow("SELECT manifest_hash FROM keyframes ORDER BY seq DESC LIMIT 1").Scan(&lastHash)
	if err == nil {
		e.current, err = manifest.Load(blob.Hash(lastHash), bs)
		if err != nil {
			db.Close()
			return nil, err
		}
	} else if err != sql.ErrNoRows {
		db.Close()
		return nil, fmt.Errorf("loading latest keyframe: %w", err)
	}
	return e, nil
}

func (e *Engine) Close() error { return e.DB.Close() }

func (e *Engine) Baseline() error {
	var n int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE seq=0").Scan(&n); err != nil {
		return fmt.Errorf("checking baseline: %w", err)
	}
	if n > 0 {
		return nil
	}
	m, err := manifest.Scan(e.Root, nil, e.Blobs)
	if err != nil {
		return err
	}
	h, err := m.Save(e.Blobs)
	if err != nil {
		return err
	}
	keyID, err := id.New()
	if err != nil {
		return err
	}
	_, err = e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,git_head,tag,created_at) VALUES(?,0,?,?,?,?)", keyID, h, e.head(), "baseline", time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("saving baseline: %w", err)
	}
	e.current = m
	return nil
}

func (e *Engine) head() string {
	out, err := exec.Command("git", "-C", e.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (e *Engine) Process(ev event.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ev = ev.Sanitized()
	switch ev.Kind {
	case event.SessionStart:
		_, err := e.session(ev)
		return err
	case event.TurnStart:
		return e.turnStart(ev)
	case event.ToolEdit:
		return e.toolEdit(ev)
	case event.FileRead:
		return e.fileRead(ev)
	case event.TurnContext:
		return e.turnContext(ev)
	case event.TurnEnd:
		return e.turnEnd(ev)
	case event.SessionEnd:
		_, err := e.DB.Exec("UPDATE sessions SET ended_at=? WHERE agent=? AND agent_sid=? AND worktree=? AND ended_at IS NULL", ev.At, ev.Agent, ev.AgentSessionID, e.Root)
		if err != nil {
			return fmt.Errorf("ending session: %w", err)
		}
		return nil
	case event.GitCommit:
		return e.gitCommit(ev)
	default:
		return nil
	}
}

func (e *Engine) session(ev event.Event) (string, error) {
	var sid string
	err := e.DB.QueryRow("SELECT id FROM sessions WHERE agent=? AND agent_sid=? AND worktree=? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1", ev.Agent, ev.AgentSessionID, e.Root).Scan(&sid)
	if err == nil {
		return sid, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("finding session: %w", err)
	}
	sid, err = id.New()
	if err != nil {
		return "", err
	}
	forkedFrom, readErr := os.ReadFile(filepath.Join(repo.TraceDir(e.Root), "forked-from"))
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("reading fork origin: %w", readErr)
	}
	_, err = e.DB.Exec("INSERT INTO sessions(id,agent,agent_sid,worktree,started_at,forked_from) VALUES(?,?,?,?,?,?)", sid, ev.Agent, ev.AgentSessionID, e.Root, ev.At, strings.TrimSpace(string(forkedFrom)))
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return sid, nil
}

func (e *Engine) turnStart(ev event.Event) error {
	sid, err := e.session(ev)
	if err != nil {
		return err
	}
	var open string
	err = e.DB.QueryRow("SELECT id FROM turns WHERE session_id=? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1", sid).Scan(&open)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("checking open turn: %w", err)
	}
	var seq int64
	if err := e.DB.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM turns").Scan(&seq); err != nil {
		return fmt.Errorf("finding turn sequence: %w", err)
	}
	tid, err := id.New()
	if err != nil {
		return err
	}
	prompt := ev.Prompt
	var agyOffset int64
	if ev.Agent == "agy" && ev.TranscriptPath != "" {
		if latest, offset, err := agy.LatestPrompt(ev.TranscriptPath); err == nil {
			if latest != "" {
				prompt = latest
			}
			agyOffset = offset
		}
	}
	_, err = e.DB.Exec("INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES(?,?,?,?,?)", tid, sid, seq, e.Redactor.Text(prompt), ev.At)
	if err != nil {
		return fmt.Errorf("creating turn: %w", err)
	}
	if (ev.Agent == "claude-code" || ev.Agent == "agy") && ev.TranscriptPath != "" {
		var offset int64
		if ev.Agent == "agy" {
			offset = agyOffset
		} else if info, err := os.Stat(ev.TranscriptPath); err == nil {
			offset = info.Size()
		}
		if _, err := e.DB.Exec(`INSERT INTO transcript_offsets(session_id,path,offset) VALUES(?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET path=excluded.path,offset=excluded.offset`, sid, ev.TranscriptPath, offset); err != nil {
			return fmt.Errorf("recording Claude transcript offset: %w", err)
		}
	}
	return nil
}

func (e *Engine) openTurn(ev event.Event) (string, error) {
	var tid string
	err := e.DB.QueryRow(`SELECT t.id FROM turns t JOIN sessions s ON s.id=t.session_id
		WHERE s.agent=? AND s.agent_sid=? AND s.worktree=? AND t.ended_at IS NULL
		ORDER BY t.started_at DESC LIMIT 1`, ev.Agent, ev.AgentSessionID, e.Root).Scan(&tid)
	if err != nil {
		return "", err
	}
	return tid, nil
}

func (e *Engine) toolEdit(ev event.Event) error {
	tid, err := e.openTurn(ev)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finding open turn: %w", err)
	}
	var sessionID string
	if err := e.DB.QueryRow("SELECT session_id FROM turns WHERE id=?", tid).Scan(&sessionID); err != nil {
		return fmt.Errorf("finding turn session: %w", err)
	}
	paths := ev.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	var firstPath string
	for _, p := range paths {
		if p != "" {
			rel, err := repo.RelativePath(e.Root, p)
			if err != nil || manifest.Excluded(rel) {
				continue
			}
			p = rel
			if firstPath == "" {
				firstPath = p
			}
		}
		opID, err := id.New()
		if err != nil {
			return err
		}
		kind := "edit"
		if p != "" {
			contested, err := e.contested(p, sessionID, ev.At)
			if err != nil {
				return err
			}
			if contested {
				kind = "contested"
			}
		}
		_, err = e.DB.Exec("INSERT INTO ops(id,turn_id,session_id,path,kind,source,at) VALUES(?,?,?,?,?,?,?)", opID, tid, sessionID, p, kind, "tool", ev.At)
		if err != nil {
			return fmt.Errorf("recording tool edit: %w", err)
		}
	}
	if ev.ToolName != "" {
		if err := e.appendToolCall(tid, ev.ToolName, firstPath); err != nil {
			return err
		}
	}
	return nil
}

type toolCallSummary struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

func (e *Engine) appendToolCall(turnID, name, path string) error {
	var current string
	if err := e.DB.QueryRow("SELECT COALESCE(tool_calls,'') FROM turns WHERE id=?", turnID).Scan(&current); err != nil {
		return fmt.Errorf("reading turn tool calls: %w", err)
	}
	var calls []toolCallSummary
	if current != "" {
		if err := json.Unmarshal([]byte(current), &calls); err != nil {
			return fmt.Errorf("parsing turn tool calls: %w", err)
		}
	}
	calls = append(calls, toolCallSummary{Name: e.Redactor.Text(name), Path: path})
	b, err := json.Marshal(calls)
	if err != nil {
		return fmt.Errorf("encoding turn tool calls: %w", err)
	}
	if _, err := e.DB.Exec("UPDATE turns SET tool_calls=? WHERE id=?", string(b), turnID); err != nil {
		return fmt.Errorf("saving turn tool calls: %w", err)
	}
	return nil
}

func (e *Engine) fileRead(ev event.Event) error {
	tid, err := e.openTurn(ev)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finding turn for read: %w", err)
	}
	var current string
	if err := e.DB.QueryRow("SELECT COALESCE(files_read,'') FROM turns WHERE id=?", tid).Scan(&current); err != nil {
		return fmt.Errorf("reading turn read paths: %w", err)
	}
	var files []string
	if current != "" {
		if err := json.Unmarshal([]byte(current), &files); err != nil {
			return fmt.Errorf("parsing turn read paths: %w", err)
		}
	}
	seen := make(map[string]bool, len(files))
	for _, path := range files {
		seen[path] = true
	}
	for _, path := range ev.Paths {
		rel, err := repo.RelativePath(e.Root, path)
		if err != nil || manifest.Excluded(rel) {
			continue
		}
		seen[rel] = true
	}
	files = files[:0]
	for path := range seen {
		files = append(files, path)
	}
	sort.Strings(files)
	b, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("encoding turn read paths: %w", err)
	}
	if _, err := e.DB.Exec("UPDATE turns SET files_read=? WHERE id=?", string(b), tid); err != nil {
		return fmt.Errorf("saving turn read paths: %w", err)
	}
	return nil
}

func (e *Engine) turnContext(ev event.Event) error {
	if ev.Text == "" {
		return nil
	}
	tid, err := e.openTurn(ev)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finding turn for context: %w", err)
	}
	var current string
	if err := e.DB.QueryRow("SELECT COALESCE(reasoning,'') FROM turns WHERE id=?", tid).Scan(&current); err != nil {
		return fmt.Errorf("reading turn context: %w", err)
	}
	label := "Assistant response"
	if ev.ContextType == "thinking" {
		label = "Thinking"
	}
	if current != "" {
		current += "\n\n"
	}
	current += label + ": " + e.Redactor.Text(ev.Text)
	if _, err := e.DB.Exec("UPDATE turns SET reasoning=? WHERE id=?", current, tid); err != nil {
		return fmt.Errorf("saving turn context: %w", err)
	}
	return nil
}

func (e *Engine) contested(path, sessionID string, at int64) (bool, error) {
	var count int
	err := e.DB.QueryRow(`SELECT COUNT(*) FROM ops WHERE path=? AND session_id IS NOT NULL
		AND session_id<>? AND at>=? AND at<=?`, path, sessionID, at-2000, at).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking concurrent edits: %w", err)
	}
	return count > 0, nil
}

func (e *Engine) turnEnd(ev event.Event) error {
	tid, err := e.openTurn(ev)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finding open turn: %w", err)
	}
	var startedAt int64
	var sessionID string
	if err := e.DB.QueryRow("SELECT started_at,session_id FROM turns WHERE id=?", tid).Scan(&startedAt, &sessionID); err != nil {
		return fmt.Errorf("finding turn timestamps: %w", err)
	}
	var reasoning, toolCalls, filesRead, transcriptPrompt string
	var writtenPaths []string
	var hasCommand bool
	var transcriptOffset int64
	var transcriptPath string
	if (ev.Agent == "claude-code" || ev.Agent == "agy") && ev.TranscriptPath != "" {
		var savedPath string
		err := e.DB.QueryRow("SELECT path,offset FROM transcript_offsets WHERE session_id=?", sessionID).Scan(&savedPath, &transcriptOffset)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("reading Claude transcript offset: %w", err)
		}
		transcriptPath = ev.TranscriptPath
		if savedPath != transcriptPath {
			transcriptOffset = 0
		}
		if ev.Agent == "claude-code" {
			context, next, readErr := claudecode.ReadTranscript(transcriptPath, transcriptOffset, e.Root)
			if readErr == nil {
				reasoning, toolCalls, filesRead, transcriptOffset = context.Reasoning, context.ToolCalls, context.FilesRead, next
			}
		} else {
			context, next, readErr := agy.ReadTranscript(transcriptPath, transcriptOffset, e.Root)
			if readErr == nil {
				reasoning, toolCalls, filesRead, transcriptPrompt = context.Reasoning, context.ToolCalls, context.FilesRead, context.Prompt
				writtenPaths, hasCommand, transcriptOffset = context.WrittenPaths, context.HasCommand, next
			}
		}
	}
	if ev.Agent == "claude-code" && ev.Text != "" && !strings.Contains(reasoning, ev.Text) {
		if reasoning != "" {
			reasoning += "\n\n"
		}
		reasoning += "Assistant response: " + e.Redactor.Text(ev.Text)
	}
	// The transcript is a best-effort source. A missing or unreadable file never
	// prevents a turn from receiving its keyframe.
	if ev.Agent == "agy" {
		if transcriptPrompt == "" && transcriptPath != "" {
			transcriptPrompt, _, _ = agy.LatestPrompt(transcriptPath)
		}
		for _, path := range writtenPaths {
			var exists int
			if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id=? AND source='tool' AND path=?", tid, path).Scan(&exists); err != nil {
				return fmt.Errorf("checking Antigravity tool edit: %w", err)
			}
			if exists == 0 {
				if err := e.toolEdit(event.Event{Agent: ev.Agent, AgentSessionID: ev.AgentSessionID, Paths: []string{filepath.Join(e.Root, path)}, At: ev.At}); err != nil {
					return err
				}
			}
		}
		if hasCommand {
			var exists int
			if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id=? AND source='tool' AND path=''", tid).Scan(&exists); err != nil {
				return fmt.Errorf("checking Antigravity command: %w", err)
			}
			if exists == 0 {
				if err := e.toolEdit(event.Event{Agent: ev.Agent, AgentSessionID: ev.AgentSessionID, At: ev.At}); err != nil {
					return err
				}
			}
		}
	}
	var seq int64
	if err := e.DB.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM keyframes").Scan(&seq); err != nil {
		return fmt.Errorf("finding keyframe sequence: %w", err)
	}
	dirty, unknown, err := e.dirtyForTurn(tid)
	if err != nil {
		return err
	}
	if unknown {
		// The watcher has already updated the manifest for these paths. Keep
		// the fallback scan when it has not observed the command's writes.
		var observed int
		if err := e.DB.QueryRow(`SELECT COUNT(*) FROM ops WHERE turn_id=? AND source='watcher' AND path<>''`, tid).Scan(&observed); err != nil {
			return fmt.Errorf("checking watcher changes for command: %w", err)
		}
		unknown = observed == 0
	}
	forcePath := filepath.Join(repo.TraceDir(e.Root), "force-scan")
	_, forceErr := os.Stat(forcePath)
	if forceErr != nil && !os.IsNotExist(forceErr) {
		return fmt.Errorf("checking full-scan marker: %w", forceErr)
	}
	full := unknown || seq%50 == 0 || forceErr == nil
	m, err := e.recordChanges(dirty, full)
	if err != nil {
		return err
	}
	h, err := m.Save(e.Blobs)
	if err != nil {
		return err
	}
	keyID, err := id.New()
	if err != nil {
		return err
	}
	tx, err := e.DB.Begin()
	if err != nil {
		return fmt.Errorf("beginning keyframe transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE ops SET turn_id=?,session_id=? WHERE turn_id IS NULL
		AND source='watcher' AND at>=? AND at<=?`, tid, sessionID, startedAt, ev.At); err != nil {
		return fmt.Errorf("attributing queued file changes: %w", err)
	}
	if _, err := tx.Exec(`UPDATE turns SET ended_at=?,prompt=CASE WHEN prompt='' THEN ? ELSE prompt END,
		reasoning=COALESCE(NULLIF(?,''),reasoning),tool_calls=COALESCE(NULLIF(?,''),tool_calls),
		files_read=COALESCE(NULLIF(?,''),files_read) WHERE id=?`,
		ev.At, e.Redactor.Text(transcriptPrompt), reasoning, toolCalls, filesRead, tid); err != nil {
		return fmt.Errorf("finishing turn: %w", err)
	}
	if transcriptPath != "" {
		if _, err := tx.Exec(`INSERT INTO transcript_offsets(session_id,path,offset) VALUES(?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET path=excluded.path,offset=excluded.offset`, sessionID, transcriptPath, transcriptOffset); err != nil {
			return fmt.Errorf("saving Claude transcript offset: %w", err)
		}
	}
	if _, err := tx.Exec("INSERT INTO keyframes(id,turn_id,seq,manifest_hash,git_head,tag,created_at) VALUES(?,?,?,?,?,?,?)", keyID, tid, seq, h, e.head(), "turn", ev.At); err != nil {
		return fmt.Errorf("saving keyframe: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing keyframe: %w", err)
	}
	if forceErr == nil {
		if err := os.Remove(forcePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clearing full-scan marker: %w", err)
		}
	}
	return nil
}

func (e *Engine) dirtyForTurn(turnID string) ([]string, bool, error) {
	rows, err := e.DB.Query("SELECT path FROM ops WHERE turn_id=? AND source='tool'", turnID)
	if err != nil {
		return nil, false, fmt.Errorf("finding turn edits: %w", err)
	}
	defer rows.Close()
	var paths []string
	unknown := false
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, false, err
		}
		if path == "" {
			unknown = true
		} else {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return paths, unknown, nil
}

func (e *Engine) gitCommit(ev event.Event) error {
	if ev.GitSHA == "" {
		return nil
	}
	var keyID string
	if err := e.DB.QueryRow("SELECT id FROM keyframes ORDER BY seq DESC LIMIT 1").Scan(&keyID); err != nil {
		return fmt.Errorf("finding latest keyframe: %w", err)
	}
	_, err := e.DB.Exec("INSERT OR IGNORE INTO commits(sha,keyframe_id,at) VALUES(?,?,?)", ev.GitSHA, keyID, ev.At)
	if err != nil {
		return fmt.Errorf("linking commit: %w", err)
	}
	return nil
}

func (e *Engine) ManifestAt(seq int64) (manifest.Manifest, error) {
	var h string
	if err := e.DB.QueryRow("SELECT manifest_hash FROM keyframes WHERE seq=?", seq).Scan(&h); err != nil {
		return nil, fmt.Errorf("finding keyframe %d: %w", seq, err)
	}
	return manifest.Load(blob.Hash(h), e.Blobs)
}

func (e *Engine) Rewind(seq int64, glob string, dryRun bool) (int64, []string, error) {
	target, err := e.ManifestAt(seq)
	if err != nil {
		return 0, nil, err
	}
	current, err := manifest.Scan(e.Root, nil, e.Blobs)
	if err != nil {
		return 0, nil, err
	}
	if glob != "" {
		if _, err := path.Match(glob, "test"); err != nil {
			return 0, nil, fmt.Errorf("invalid file glob: %w", err)
		}
		limited := manifest.Manifest{}
		for p, entry := range current {
			limited[p] = entry
		}
		for p := range current {
			if match, _ := path.Match(glob, p); match {
				delete(limited, p)
			}
		}
		for p, entry := range target {
			if match, _ := path.Match(glob, p); match {
				limited[p] = entry
			}
		}
		target = limited
	}
	changed := restore.Plan(current, target)
	if dryRun || len(changed) == 0 {
		return 0, changed, nil
	}
	h, err := current.Save(e.Blobs)
	if err != nil {
		return 0, nil, err
	}
	var safetySeq int64
	if err := e.DB.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM keyframes").Scan(&safetySeq); err != nil {
		return 0, nil, fmt.Errorf("finding safety sequence: %w", err)
	}
	keyID, err := id.New()
	if err != nil {
		return 0, nil, err
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,git_head,tag,created_at) VALUES(?,?,?,?,?,?)", keyID, safetySeq, h, e.head(), "pre-rewind", time.Now().UnixMilli()); err != nil {
		return 0, nil, fmt.Errorf("saving safety keyframe: %w", err)
	}
	changed, err = restore.Apply(target, e.Root, nil, e.Blobs)
	if err != nil {
		return safetySeq, nil, err
	}
	if _, err := e.recordChanges(nil, true); err != nil {
		return safetySeq, changed, err
	}
	opID, err := id.New()
	if err != nil {
		return safetySeq, changed, err
	}
	if _, err := e.DB.Exec("INSERT INTO ops(id,path,kind,source,at) VALUES(?,?,?,?,?)", opID, "", "rewind", "user", time.Now().UnixMilli()); err != nil {
		return safetySeq, changed, fmt.Errorf("recording rewind: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repo.TraceDir(e.Root), "force-scan"), []byte("1\n"), 0600); err != nil {
		return safetySeq, changed, fmt.Errorf("marking full scan after rewind: %w", err)
	}
	return safetySeq, changed, nil
}
