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
	"syscall"
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
		return replaceDamagedMarkerKey(path, name, key)
	}
	return publishMarkerKey(path, name, key)
}

// publishMarkerKey links the complete key file name at path. A link never
// replaces an existing file, so when another process published first this
// one adopts that key instead.
func publishMarkerKey(path, name string, key []byte) ([]byte, error) {
	if err := os.Link(name, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readMarkerKey(path)
		}
		return nil, fmt.Errorf("publishing marker key: %w", err)
	}
	return key, nil
}

// replaceDamagedMarkerKey replaces a damaged key with the one in name. It
// follows the same protocol as trace-brain's copy (internal/redact/key.go):
// an flock on path+".lock", a reread, the damaged file moved to
// path+".damaged", and a linked publish, so the two components interoperate.
// Replacing is the only step that removes a key, so repairs are serialized
// by a lock next to the key, and each one first rereads the key: a process
// that waited adopts the key a previous repair published. The damaged file
// is moved aside and the new key is published with a link, like a first
// key, so a process that found the key missing in between wins instead.
// Every process thus ends up with the same key.
func replaceDamagedMarkerKey(path, name string, key []byte) ([]byte, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening marker key lock: %w", err)
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("locking marker key: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	current, err := readMarkerKey(path)
	switch {
	case err == nil:
		return current, nil
	case errors.Is(err, errDamagedMarkerKey):
		if err := os.Rename(path, path+".damaged"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("moving damaged marker key aside: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	return publishMarkerKey(path, name, key)
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

// CheckMarkerKey reports whether the per-install marker key exists as a
// private regular file this process can read. When it cannot be used, every
// process redacts with its own ephemeral key, so equal secrets get different
// markers. It never creates or repairs the key.
func CheckMarkerKey() (string, error) {
	path, err := userstate.Path(markerKeyFile)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, fmt.Errorf("%s is missing; run trace init or start the daemon to create it", path)
	}
	if err != nil {
		return path, err
	}
	if !info.Mode().IsRegular() {
		return path, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return path, fmt.Errorf("%s has mode %04o; want 0600", path, info.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		return path, fmt.Errorf("%s is not readable: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return path, fmt.Errorf("reading %s: %w", path, err)
	}
	if key, err := hex.DecodeString(strings.TrimSpace(string(data))); err != nil || len(key) != 32 {
		return path, fmt.Errorf("%s is damaged", path)
	}
	return path, nil
}
