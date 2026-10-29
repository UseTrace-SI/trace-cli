package syncclient

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

var downloadID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var downloadHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

var columns = map[string][]string{
	"sessions":     {"id", "agent", "agent_sid", "worktree", "started_at", "ended_at", "forked_from"},
	"turns":        {"id", "session_id", "seq", "prompt", "reasoning", "tool_calls", "files_read", "started_at", "ended_at"},
	"keyframes":    {"id", "turn_id", "seq", "manifest_hash", "git_head", "tag", "created_at"},
	"ops":          {"id", "turn_id", "session_id", "path", "kind", "start_line", "end_line", "before_hash", "after_hash", "source", "at", "detail"},
	"commits":      {"sha", "keyframe_id", "at"},
	"messages":     {"id", "session_id", "turn_id", "role", "content", "at"},
	"sync_updates": {"id", "table_name", "row_id", "data"},
}

func applyDownloadedUpdate(db *sql.DB, row Row, plain []byte) error {
	var update struct {
		ID        string          `json:"id"`
		TableName string          `json:"table_name"`
		RowID     string          `json:"row_id"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(plain, &update); err != nil {
		return fmt.Errorf("decoding downloaded update: %w", err)
	}
	if update.ID != row.ID || !downloadID.MatchString(update.RowID) {
		return errors.New("downloaded update has an invalid ID")
	}
	var snapshot map[string]any
	if err := json.Unmarshal(update.Data, &snapshot); err != nil || snapshot["id"] != update.RowID {
		return errors.New("downloaded update has invalid row data")
	}
	var fields []string
	switch update.TableName {
	case "sessions":
		fields = []string{"ended_at"}
	case "turns":
		fields = []string{"prompt", "reasoning", "tool_calls", "files_read", "ended_at"}
	case "ops":
		fields = []string{"turn_id", "session_id"}
	default:
		return errors.New("downloaded update names an invalid table")
	}
	values := make([]any, 0, len(fields)+1)
	assignments := make([]string, 0, len(fields))
	for _, field := range fields {
		value, ok := snapshot[field]
		if !ok {
			return fmt.Errorf("downloaded update is missing %s", field)
		}
		if text, ok := value.(string); ok && (field == "prompt" || field == "reasoning" || field == "tool_calls" || field == "files_read") {
			value = (redact.Redactor{}).Text(text)
			snapshot[field] = value
		}
		assignments = append(assignments, field+"=?")
		values = append(values, value)
	}
	values = append(values, update.RowID)
	result, err := db.Exec("UPDATE "+update.TableName+" SET "+strings.Join(assignments, ",")+" WHERE id=?", values...)
	if err != nil {
		return fmt.Errorf("applying downloaded update: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("downloaded update references a missing row")
	}
	sanitized, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encoding downloaded update: %w", err)
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO sync_updates(id,table_name,row_id,data) VALUES(?,?,?,?)", update.ID, update.TableName, update.RowID, string(sanitized)); err != nil {
		return fmt.Errorf("storing downloaded update: %w", err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, output any) error {
	u := strings.TrimRight(c.Config.Endpoint, "/") + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("creating download request: %w", err)
	}
	token, err := c.authorizationToken(ctx)
	if err != nil {
		return fmt.Errorf("refreshing GitHub login: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("downloading sync data: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync server rejected download: %s", resp.Status)
	}
	// The Worker returns up to 20 rows, each with a payload near 1 MB.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(output); err != nil {
		return fmt.Errorf("decoding sync download: %w", err)
	}
	return nil
}

func (c *Client) downloadValues() url.Values {
	return url.Values{"repo": {c.Config.Remote}, "mode": {c.Config.Mode}}
}

func (c *Client) openDownloaded(encoded, salt, aad, passphrase string, ciphers map[string]*Cipher) ([]byte, error) {
	if c.Config.Mode == "team" {
		return base64.StdEncoding.DecodeString(encoded)
	}
	if salt == "" {
		return nil, errors.New("pro download is missing its salt")
	}
	cipher := ciphers[salt]
	if cipher == nil {
		b, err := base64.StdEncoding.DecodeString(salt)
		if err != nil {
			return nil, fmt.Errorf("decoding Pro salt: %w", err)
		}
		cipher, err = Derive(passphrase, b)
		if err != nil {
			return nil, err
		}
		ciphers[salt] = cipher
	}
	return cipher.Open(encoded, aad)
}

func insertDownloaded(db *sql.DB, row Row, plain []byte) error {
	cols, ok := columns[row.Table]
	if !ok || !downloadID.MatchString(row.ID) {
		return errors.New("download contains an invalid row")
	}
	if row.Table == "sync_updates" {
		return applyDownloadedUpdate(db, row, plain)
	}
	var data map[string]any
	if err := json.Unmarshal(plain, &data); err != nil {
		return fmt.Errorf("decoding downloaded row: %w", err)
	}
	key := "id"
	if row.Table == "commits" {
		key = "sha"
	}
	if data[key] != row.ID {
		return errors.New("download row ID mismatch")
	}
	// Pull is retried after partial failures; re-applying an already stored row
	// must be a no-op instead of a primary-key error.
	var exists int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+row.Table+" WHERE "+key+"=?", row.ID).Scan(&exists); err != nil {
		return fmt.Errorf("checking downloaded row: %w", err)
	}
	if exists > 0 {
		return nil
	}
	if row.Table == "turns" || row.Table == "keyframes" {
		seq, ok := data["seq"].(float64)
		if !ok || seq < 0 || seq != math.Trunc(seq) {
			return errors.New("download contains an invalid step")
		}
		var duplicate int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+row.Table+" WHERE seq=?", seq).Scan(&duplicate); err != nil {
			return fmt.Errorf("checking downloaded step: %w", err)
		}
		if duplicate > 0 {
			var next int64
			if err := db.QueryRow("SELECT COALESCE(MAX(seq), -1)+1 FROM " + row.Table).Scan(&next); err != nil {
				return fmt.Errorf("assigning recovered step: %w", err)
			}
			data["seq"] = next
		}
	}
	for _, field := range []string{"prompt", "reasoning", "tool_calls", "files_read", "detail", "content"} {
		if value, ok := data[field].(string); ok {
			data[field] = (redact.Redactor{}).Text(value)
		}
	}
	values := make([]any, len(cols))
	for i, col := range cols {
		values[i] = data[col]
	}
	query := "INSERT INTO " + row.Table + " (" + strings.Join(cols, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
	if _, err := db.Exec(query, values...); err != nil {
		return fmt.Errorf("inserting downloaded %s row: %w", row.Table, err)
	}
	return nil
}

func (c *Client) pullRows(ctx context.Context, db *sql.DB, passphrase string, ciphers map[string]*Cipher) (int, error) {
	total := 0
	for _, table := range tables {
		after := ""
		for {
			query := c.downloadValues()
			query.Set("table", table)
			query.Set("after", after)
			var page struct {
				Rows []Row   `json:"rows"`
				Next *string `json:"next"`
			}
			if err := c.get(ctx, "/v1/sync/rows", query, &page); err != nil {
				return total, err
			}
			if len(page.Rows) > 20 {
				return total, errors.New("sync server returned too many rows")
			}
			for _, row := range page.Rows {
				if row.Table != table || !downloadID.MatchString(row.ID) || row.ID <= after {
					return total, errors.New("sync server returned an invalid row cursor")
				}
				var plain []byte
				var err error
				if c.Config.Mode == "pro" {
					plain, err = c.openDownloaded(row.Ciphertext, row.Salt, table+":"+row.ID, passphrase, ciphers)
				} else {
					plain = row.Data
				}
				if err != nil {
					return total, fmt.Errorf("decrypting downloaded row: %w", err)
				}
				if err := insertDownloaded(db, row, plain); err != nil {
					return total, err
				}
				after = row.ID
				total++
			}
			if page.Next == nil {
				break
			}
			if len(page.Rows) == 0 || *page.Next != after {
				return total, errors.New("sync server returned an invalid next cursor")
			}
		}
	}
	return total, nil
}

func (c *Client) pullBlobs(ctx context.Context, bs *blob.Store, passphrase string, ciphers map[string]*Cipher) (int, error) {
	after := ""
	total := 0
	for {
		query := c.downloadValues()
		query.Set("after", after)
		var page struct {
			Hashes []string `json:"hashes"`
			Next   *string  `json:"next"`
		}
		if err := c.get(ctx, "/v1/sync/blobs/list", query, &page); err != nil {
			return total, err
		}
		if len(page.Hashes) > 100 {
			return total, errors.New("sync server returned too many blob hashes")
		}
		for _, hash := range page.Hashes {
			if !downloadHash.MatchString(hash) || hash <= after {
				return total, errors.New("sync server returned an invalid blob cursor")
			}
			query := c.downloadValues()
			query.Set("hash", hash)
			var item struct {
				Hash    string `json:"hash"`
				Payload string `json:"payload"`
				Salt    string `json:"salt"`
			}
			if err := c.get(ctx, "/v1/sync/blobs", query, &item); err != nil {
				return total, err
			}
			if item.Hash != hash {
				return total, errors.New("sync server returned the wrong blob")
			}
			plain, err := c.openDownloaded(item.Payload, item.Salt, "blob:"+hash, passphrase, ciphers)
			if err != nil {
				return total, fmt.Errorf("decrypting downloaded blob: %w", err)
			}
			stored, err := bs.PutBytes(plain)
			if err != nil {
				return total, err
			}
			if string(stored) != hash {
				return total, errors.New("downloaded blob hash mismatch")
			}
			after = hash
			total++
		}
		if page.Next == nil {
			break
		}
		if len(page.Hashes) == 0 || *page.Next != after {
			return total, errors.New("sync server returned an invalid blob next cursor")
		}
	}
	return total, nil
}

// Pull creates a new Trace history in an existing Git checkout. It never replaces
// an existing .trace directory or changes working-tree files.
func (c *Client) Pull(ctx context.Context, traceDir, passphrase string) (int, int, error) {
	if err := c.Config.Validate(); err != nil {
		return 0, 0, err
	}
	if c.Token == "" {
		return 0, 0, errors.New("sync download requires login")
	}
	if c.Config.Mode == "pro" && len(passphrase) < 12 {
		return 0, 0, errors.New("pro download requires TRACE_SYNC_PASSPHRASE")
	}
	if err := os.Mkdir(traceDir, 0700); err != nil {
		return 0, 0, fmt.Errorf("creating recovery directory: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(traceDir)
		}
	}()
	stage := filepath.Join(traceDir, "staging")
	if err := os.Mkdir(stage, 0700); err != nil {
		return 0, 0, err
	}
	db, err := store.Open(filepath.Join(stage, "trace.db"))
	if err != nil {
		return 0, 0, err
	}
	bs, err := blob.Open(filepath.Join(stage, "objects"))
	if err != nil {
		db.Close()
		return 0, 0, err
	}
	ciphers := map[string]*Cipher{}
	rows, err := c.pullRows(ctx, db.DB, passphrase, ciphers)
	if err != nil {
		db.Close()
		return 0, 0, err
	}
	blobs, err := c.pullBlobs(ctx, bs, passphrase, ciphers)
	if err != nil {
		db.Close()
		return 0, 0, err
	}
	var badTable string
	err = db.QueryRow("SELECT `table` FROM pragma_foreign_key_check LIMIT 1").Scan(&badTable)
	if err == nil {
		db.Close()
		return 0, 0, fmt.Errorf("recovered history has a broken reference in %s", badTable)
	}
	if err != sql.ErrNoRows {
		db.Close()
		return 0, 0, fmt.Errorf("checking recovered references: %w", err)
	}
	var keyframes int
	if err := db.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&keyframes); err != nil || keyframes == 0 {
		db.Close()
		return 0, 0, errors.New("download has no keyframes")
	}
	referenced, err := referencedHashes(db.DB, bs)
	if err != nil {
		db.Close()
		return 0, 0, fmt.Errorf("checking recovered blobs: %w", err)
	}
	for _, hash := range referenced {
		if !bs.Has(blob.Hash(hash)) {
			db.Close()
			return 0, 0, fmt.Errorf("download is missing blob %s", hash)
		}
	}
	if err := db.Close(); err != nil {
		return 0, 0, fmt.Errorf("closing recovery database: %w", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range entries {
		if entry.Name() == "trace.db" {
			continue
		}
		if err := os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(traceDir, entry.Name())); err != nil {
			return 0, 0, err
		}
	}
	if err := os.Rename(filepath.Join(stage, "trace.db"), filepath.Join(traceDir, "trace.db")); err != nil {
		return 0, 0, err
	}
	if err := os.Remove(stage); err != nil {
		return 0, 0, err
	}
	success = true
	return blobs, rows, nil
}
