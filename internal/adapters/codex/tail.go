package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type cursor struct {
	Offset    int64  `json:"offset"`
	SessionID string `json:"session_id"`
	Active    bool   `json:"active"`
}

func Run(ctx context.Context, root, sessionsDir string, process func(event.Event) error) error {
	statePath := filepath.Join(root, ".trace", "codex-offsets.json")
	cursors := map[string]cursor{}
	data, err := os.ReadFile(statePath)
	fresh := os.IsNotExist(err)
	if err != nil && !fresh {
		return fmt.Errorf("reading Codex offsets: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cursors); err != nil {
			return fmt.Errorf("parsing Codex offsets: %w", err)
		}
	}
	scan := func() error {
		err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			cur, known := cursors[path]
			if fresh && !known {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				line, _ := bufio.NewReader(f).ReadBytes('\n')
				f.Close()
				parser := Parser{Root: root}
				_, _ = parser.Feed(line)
				cursors[path] = cursor{Offset: info.Size(), SessionID: parser.SessionID, Active: parser.Active}
				return nil
			}
			if info.Size() <= cur.Offset {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := f.Seek(cur.Offset, io.SeekStart); err != nil {
				return err
			}
			reader := bufio.NewReader(f)
			parser := Parser{Root: root, SessionID: cur.SessionID, Active: cur.Active}
			for {
				line, err := reader.ReadBytes('\n')
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				events, err := parser.Feed(line)
				if err == nil {
					for _, ev := range events {
						if err := process(ev); err != nil {
							return err
						}
					}
				}
				cur.Offset += int64(len(line))
			}
			cur.SessionID = parser.SessionID
			cur.Active = parser.Active
			cursors[path] = cur
			return nil
		})
		if err != nil {
			return fmt.Errorf("scanning Codex sessions: %w", err)
		}
		b, err := json.Marshal(cursors)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(statePath), ".codex-offsets-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(b); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), statePath)
	}
	if err := scan(); err != nil {
		return err
	}
	fresh = false
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := scan(); err != nil {
				return err
			}
		}
	}
}
