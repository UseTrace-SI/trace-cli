package redact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/userstate"
)

// markerKeyFile holds the per-install key that makes redaction markers
// unguessable. A marker built from an unsalted hash of the secret lets anyone
// holding the history confirm a guessed secret, or brute-force a short one.
const markerKeyFile = "redaction.key"

var markerKey struct {
	once sync.Once
	key  []byte
}

// marker labels a redacted value. Its 8 hex digits are a keyed digest of the
// value, so equal secrets carry equal markers within one installation.
func marker(kind, value string) string {
	mac := hmac.New(sha256.New, currentMarkerKey())
	mac.Write([]byte(value))
	return "⟨redacted:" + kind + ":" + hex.EncodeToString(mac.Sum(nil))[:8] + "⟩"
}

func currentMarkerKey() []byte {
	markerKey.once.Do(func() {
		// Tests must not write into the real home directory; a per-process key
		// keeps their markers stable within a run.
		if !testing.Testing() {
			if path, err := userstate.Path(markerKeyFile); err == nil {
				if key, err := loadMarkerKey(path); err == nil {
					markerKey.key = key
					return
				}
			}
		}
		// Without a readable state directory, redaction must still work: an
		// ephemeral key keeps markers unguessable, though not stable across
		// processes.
		markerKey.key = randomMarkerKey()
	})
	return markerKey.key
}

// EnsureMarkerKey creates the per-install marker key ahead of the first
// redaction, so a latency-sensitive hook rarely has to.
func EnsureMarkerKey() { currentMarkerKey() }

func randomMarkerKey() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key) // crypto/rand.Read never fails on supported platforms.
	return key
}

// loadMarkerKey reads the key at path, creating it with mode 0600 when it is
// missing or damaged. Concurrent first runs agree on one key: a new key is
// published with a hard link, which never replaces a key another process
// published first.
func loadMarkerKey(path string) ([]byte, error) {
	key, err := readMarkerKey(path)
	if err == nil {
		return key, nil
	}
	damaged := !errors.Is(err, os.ErrNotExist)
	if damaged && !errors.Is(err, errDamagedMarkerKey) {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	key = randomMarkerKey()
	tmp, err := os.CreateTemp(dir, markerKeyFile+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("creating marker key: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("restricting marker key: %w", err)
	}
	if _, err := tmp.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("writing marker key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("syncing marker key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("closing marker key: %w", err)
	}
	if damaged {
		// A damaged key protects nothing; replace it atomically.
		if err := os.Rename(name, path); err != nil {
			return nil, fmt.Errorf("replacing damaged marker key: %w", err)
		}
		return key, nil
	}
	if err := os.Link(name, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readMarkerKey(path)
		}
		return nil, fmt.Errorf("publishing marker key: %w", err)
	}
	return key, nil
}

var errDamagedMarkerKey = errors.New("marker key is damaged")

func readMarkerKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("marker key %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info.Mode().Perm()&0077 != 0 {
		_ = f.Chmod(0600)
	}
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, errDamagedMarkerKey
	}
	return key, nil
}
