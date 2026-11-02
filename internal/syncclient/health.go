package syncclient

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/redact"
)

type Health struct {
	ConnectionID string `json:"connection_id,omitempty"`
	LastAttempt  int64  `json:"last_attempt"`
	LastSuccess  int64  `json:"last_success"`
	LastError    string `json:"last_error,omitempty"`
	Action       string `json:"action,omitempty"`
}

func ReadHealth(dir string) (Health, error) {
	var h Health
	b, err := os.ReadFile(filepath.Join(dir, "sync-health.json"))
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	err = json.Unmarshal(b, &h)
	return h, err
}

func RecordAttempt(dir string, cfg Config, attemptErr error) error {
	if dir == "" {
		return errors.New("sync directory is required")
	}
	h, _ := ReadHealth(dir)
	if h.ConnectionID != cfg.ConnectionID {
		h = Health{ConnectionID: cfg.ConnectionID}
	}
	h.LastAttempt = time.Now().UnixMilli()
	h.LastError = ""
	h.Action = ""
	if attemptErr == nil {
		h.LastSuccess = h.LastAttempt
	} else {
		message := (redact.Redactor{}).Text(attemptErr.Error())
		if len(message) > 1024 {
			message = message[:1024]
		}
		h.LastError = message
		h.Action = "Run trace sync to retry; run trace connect again if the device is expired or revoked."
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".sync-health-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "sync-health.json"))
}
