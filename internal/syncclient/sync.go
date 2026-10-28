package syncclient

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

const batchSize = 500
const maxRowRequestBytes = 7 << 20

var tables = []string{"sessions", "turns", "keyframes", "ops", "commits"}

type Config struct {
	Endpoint string `json:"endpoint"`
	Remote   string `json:"remote"`
	Mode     string `json:"mode"`
}

func (cfg Config) Validate() error {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("sync endpoint must be an HTTPS URL without credentials or query")
	}
	if cfg.Mode != "pro" && cfg.Mode != "team" {
		return errors.New("sync mode must be pro or team")
	}
	if cfg.Remote == "" {
		return errors.New("sync remote is required")
	}
	return nil
}

type State struct {
	Target  string            `json:"target"`
	Cursors map[string]string `json:"cursors"`
}

type Row struct {
	Table      string          `json:"table"`
	ID         string          `json:"id"`
	Data       json.RawMessage `json:"data,omitempty"`
	Ciphertext string          `json:"ciphertext,omitempty"`
	Salt       string          `json:"salt,omitempty"`
}

type Client struct {
	HTTP     *http.Client
	Config   Config
	Token    string
	Cipher   *Cipher
	TraceDir string
	DB       *sql.DB
	Blobs    *blob.Store
}

func (c *Client) validate() error {
	if err := c.Config.Validate(); err != nil {
		return err
	}
	if c.Config.Mode == "pro" && c.Cipher == nil {
		return errors.New("pro sync requires a local encryption key")
	}
	if c.Token == "" || c.DB == nil || c.Blobs == nil {
		return errors.New("sync requires login, database, and blob store")
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (c *Client) post(ctx context.Context, path string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encoding sync request: %w", err)
	}
	u := strings.TrimRight(c.Config.Endpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("creating sync request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("sending sync request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync server rejected request: %s", resp.Status)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output); err != nil {
			return fmt.Errorf("decoding sync response: %w", err)
		}
	}
	return nil
}

func loadState(path string) (State, error) {
	s := State{Cursors: map[string]string{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading sync state: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("decoding sync state: %w", err)
	}
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	return s, nil
}

func saveState(path string, state State) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sync-state-*")
	if err != nil {
		return fmt.Errorf("creating sync state: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("saving sync state: %w", err)
	}
	return nil
}

// Sync uploads content-addressed blobs first, then append-only rows. Each row batch
// advances its local cursor only after the server acknowledges the request.
func (c *Client) Sync(ctx context.Context) (int, int, error) {
	if err := c.validate(); err != nil {
		return 0, 0, err
	}
	statePath := filepath.Join(c.TraceDir, "sync-state.json")
	state, err := loadState(statePath)
	if err != nil {
		return 0, 0, err
	}
	target := c.Config.Endpoint + "|" + c.Config.Remote + "|" + c.Config.Mode
	if state.Target != target {
		state = State{Target: target, Cursors: map[string]string{}}
	}
	blobs, err := c.syncBlobs(ctx)
	if err != nil {
		return 0, 0, err
	}
	rowCount := 0
	for _, table := range tables {
		for {
			rows, cursors, err := readRows(c.DB, table, state.Cursors[table], batchSize)
			if err != nil {
				return blobs, rowCount, err
			}
			if len(rows) == 0 {
				break
			}
			if c.Cipher != nil {
				for i := range rows {
					rows[i].Ciphertext, err = c.Cipher.Seal(rows[i].Data, rows[i].Table+":"+rows[i].ID)
					if err != nil {
						return blobs, rowCount, err
					}
					rows[i].Data = nil
				}
			}
			request := map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "rows": []Row{}}
			if c.Cipher != nil {
				request["salt"] = base64.StdEncoding.EncodeToString(c.Cipher.Salt)
			}
			header, err := json.Marshal(request)
			if err != nil {
				return blobs, rowCount, fmt.Errorf("encoding sync batch header: %w", err)
			}
			for start := 0; start < len(rows); {
				end, err := rowBatchEnd(rows, start, len(header))
				if err != nil {
					return blobs, rowCount, err
				}
				request["rows"] = rows[start:end]
				if err := c.post(ctx, "/v1/sync/rows", request, nil); err != nil {
					return blobs, rowCount, err
				}
				state.Cursors[table] = cursors[end-1]
				if err := saveState(statePath, state); err != nil {
					return blobs, rowCount, err
				}
				rowCount += end - start
				start = end
			}
			if len(rows) < batchSize {
				break
			}
		}
	}
	return blobs, rowCount, nil
}

func rowBatchEnd(rows []Row, start, headerBytes int) (int, error) {
	size := headerBytes
	end := start
	for end < len(rows) {
		encoded, err := json.Marshal(rows[end])
		if err != nil {
			return 0, fmt.Errorf("encoding sync row: %w", err)
		}
		additional := len(encoded)
		if end > start {
			additional++ // comma between rows
		}
		if size+additional > maxRowRequestBytes {
			break
		}
		size += additional
		end++
	}
	if end == start {
		return 0, fmt.Errorf("sync row %s exceeds the request size limit", rows[start].ID)
	}
	return end, nil
}

func readRows(db *sql.DB, table, after string, limit int) ([]Row, []string, error) {
	valid := false
	for _, t := range tables {
		if table == t {
			valid = true
			break
		}
	}
	if !valid {
		return nil, nil, errors.New("invalid sync table")
	}
	query := "SELECT * FROM " + table + " WHERE id > ? ORDER BY id LIMIT ?"
	args := []any{after, limit}
	if table == "commits" {
		query = "SELECT * FROM commits ORDER BY at, sha"
		args = nil
	}
	result, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s for sync: %w", table, err)
	}
	defer result.Close()
	columns, err := result.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out []Row
	var cursors []string
	for result.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := result.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		data := make(map[string]any, len(columns))
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				data[col] = string(b)
			} else {
				data[col] = values[i]
			}
		}
		id, _ := data["id"].(string)
		cursor := id
		if table == "commits" {
			id, _ = data["sha"].(string)
			cursor = fmt.Sprintf("%020d:%s", data["at"], id)
			if cursor <= after {
				continue
			}
		}
		b, err := json.Marshal(data)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, Row{Table: table, ID: id, Data: b})
		cursors = append(cursors, cursor)
		if len(out) == limit {
			break
		}
	}
	if err := result.Err(); err != nil {
		return nil, nil, err
	}
	return out, cursors, nil
}

func (c *Client) syncBlobs(ctx context.Context) (int, error) {
	hashes, err := referencedHashes(c.DB, c.Blobs)
	if err != nil {
		return 0, err
	}
	uploaded := 0
	for start := 0; start < len(hashes); start += batchSize {
		end := min(start+batchSize, len(hashes))
		var reply struct {
			Need []string `json:"need"`
		}
		if err := c.post(ctx, "/v1/sync/blobs/have", map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "hashes": hashes[start:end]}, &reply); err != nil {
			return uploaded, err
		}
		allowed := make(map[string]bool, end-start)
		for _, h := range hashes[start:end] {
			allowed[h] = true
		}
		for _, h := range reply.Need {
			if !allowed[h] {
				return uploaded, errors.New("sync server requested an unknown blob")
			}
			r, err := c.Blobs.Get(blob.Hash(h))
			if err != nil {
				return uploaded, err
			}
			plain, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				return uploaded, err
			}
			encoded := base64.StdEncoding.EncodeToString(plain)
			if c.Cipher != nil {
				encoded, err = c.Cipher.Seal(plain, "blob:"+h)
				if err != nil {
					return uploaded, err
				}
			}
			request := map[string]any{"repo": c.Config.Remote, "mode": c.Config.Mode, "hash": h, "payload": encoded}
			if c.Cipher != nil {
				request["salt"] = base64.StdEncoding.EncodeToString(c.Cipher.Salt)
			}
			if err := c.post(ctx, "/v1/sync/blobs", request, nil); err != nil {
				return uploaded, err
			}
			uploaded++
		}
	}
	return uploaded, nil
}

func referencedHashes(db *sql.DB, bs *blob.Store) ([]string, error) {
	marked := map[string]bool{}
	rows, err := db.Query("SELECT manifest_hash FROM keyframes")
	if err != nil {
		return nil, fmt.Errorf("reading keyframes for sync: %w", err)
	}
	var manifests []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, err
		}
		marked[h] = true
		manifests = append(manifests, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, h := range manifests {
		m, err := manifest.Load(blob.Hash(h), bs)
		if err != nil {
			return nil, err
		}
		for _, entry := range m {
			marked[string(entry.Hash)] = true
		}
	}
	rows, err = db.Query("SELECT COALESCE(before_hash,''), COALESCE(after_hash,'') FROM ops")
	if err != nil {
		return nil, fmt.Errorf("reading operations for sync: %w", err)
	}
	for rows.Next() {
		var before, after string
		if err := rows.Scan(&before, &after); err != nil {
			rows.Close()
			return nil, err
		}
		if before != "" {
			marked[before] = true
		}
		if after != "" {
			marked[after] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	hashes := make([]string, 0, len(marked))
	for h := range marked {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	return hashes, nil
}
