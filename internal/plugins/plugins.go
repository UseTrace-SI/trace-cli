// Package plugins installs explicitly selected local bundles. Publisher keys
// are pinned separately; no network request or automatic execution occurs.
package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const maxBinary = 32 * 1024 * 1024

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Manifest struct {
	Format    int    `json:"format"`
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	SHA256    string `json:"sha256"`
	Bytes     int    `json:"bytes"`
}
type Receipt struct {
	Manifest             Manifest `json:"manifest"`
	ManifestSHA256       string   `json:"manifest_sha256"`
	PublisherFingerprint string   `json:"publisher_fingerprint"`
}
type Manager struct{ Root string }

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func versionCompare(a, b string) int {
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range aa {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func ValidateManifest(manifest Manifest) error {
	if manifest.Format != 1 || !namePattern.MatchString(manifest.Name) || !namePattern.MatchString(manifest.Publisher) || !versionPattern.MatchString(manifest.Version) || !digestPattern.MatchString(manifest.SHA256) || manifest.Bytes < 1 || manifest.Bytes > maxBinary {
		return fmt.Errorf("invalid plugin manifest")
	}
	if manifest.Name == "brain" || manifest.Name == "graph" || manifest.Name == "plugin" || manifest.Name == "trace" {
		return fmt.Errorf("plugin name is reserved")
	}
	if manifest.OS != runtime.GOOS || manifest.Arch != runtime.GOARCH {
		return fmt.Errorf("plugin platform does not match this computer")
	}
	return nil
}
func parseManifest(data []byte) (Manifest, error) {
	var result Manifest
	if len(data) > 8192 {
		return result, fmt.Errorf("plugin manifest exceeds 8 KiB")
	}
	// Reject duplicate keys before typed decoding; the signature covers the exact
	// original bytes, so publishers do not need a custom canonical JSON encoder.
	tokens := json.NewDecoder(bytes.NewReader(data))
	first, err := tokens.Token()
	if err != nil || first != json.Delim('{') {
		return result, fmt.Errorf("invalid plugin manifest")
	}
	seen := map[string]bool{}
	for tokens.More() {
		key, err := tokens.Token()
		if err != nil {
			return result, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return result, fmt.Errorf("duplicate plugin manifest field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := tokens.Decode(&value); err != nil {
			return result, err
		}
	}
	if _, err := tokens.Token(); err != nil {
		return result, err
	}
	if _, err := tokens.Token(); err != io.EOF {
		return result, fmt.Errorf("extra plugin manifest data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	return result, ValidateManifest(result)
}
func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
func privateDirectory(path string, create bool) error {
	if create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return fmt.Errorf("plugin directory must be private, owned and not a symlink")
	}
	return nil
}
func regular(root *os.Root, name string, limit int) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > int64(limit) {
		return nil, fmt.Errorf("plugin input must be a bounded regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("plugin input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("plugin file exceeds budget")
	}
	return data, nil
}
func (manager Manager) check(create bool) error {
	if !filepath.IsAbs(manager.Root) || filepath.Clean(manager.Root) != manager.Root {
		return fmt.Errorf("plugin root must be canonical and absolute")
	}
	// Existing user-state parents may be readable, but must not allow another
	// user to replace this private manager directory.
	parent := filepath.Dir(manager.Root)
	if create {
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !owned(info) {
		return fmt.Errorf("plugin state parent is not safely owned")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return fmt.Errorf("plugin state path must not contain symlinks")
	}
	if err := privateDirectory(manager.Root, create); err != nil {
		return err
	}
	for _, name := range []string{"trusted", "active", "releases"} {
		if err := privateDirectory(filepath.Join(manager.Root, name), create); err != nil {
			return err
		}
	}
	return nil
}
func (manager Manager) lock() (func(), error) {
	if err := manager.check(true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(manager.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if info, err := root.Lstat("manager.lock"); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("plugin lock must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := root.OpenFile("manager.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, fmt.Errorf("plugin lock is not safely owned")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another plugin operation is active")
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}
func atomicFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".plugin-stage-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func (manager Manager) readKey(publisher string) (ed25519.PublicKey, error) {
	if !namePattern.MatchString(publisher) {
		return nil, fmt.Errorf("invalid publisher")
	}
	root, err := os.OpenRoot(filepath.Join(manager.Root, "trusted"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := regular(root, publisher+".pub", 128)
	if err != nil {
		return nil, fmt.Errorf("publisher is not explicitly trusted")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid trusted publisher key")
	}
	return ed25519.PublicKey(key), nil
}
func (manager Manager) Trust(publisher string, key []byte, replace bool) (string, error) {
	if !namePattern.MatchString(publisher) || len(key) != ed25519.PublicKeySize {
		return "", fmt.Errorf("expected publisher name and 32-byte Ed25519 public key")
	}
	unlock, err := manager.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	old, err := manager.readKey(publisher)
	if err != nil {
		if _, existsErr := os.Lstat(filepath.Join(manager.Root, "trusted", publisher+".pub")); existsErr == nil {
			return "", fmt.Errorf("existing publisher record is invalid; explicitly untrust it before replacing")
		} else if !os.IsNotExist(existsErr) {
			return "", existsErr
		}
	}
	if err == nil && !bytes.Equal(old, key) && !replace {
		return "", fmt.Errorf("publisher key differs; inspect its fingerprint and explicitly use --replace to revoke the old key")
	}
	if err != nil {
		entries, listErr := os.ReadDir(filepath.Join(manager.Root, "trusted"))
		if listErr != nil {
			return "", listErr
		}
		if len(entries) >= 64 {
			return "", fmt.Errorf("publisher trust registry reached 64 keys")
		}
	}
	if err := atomicFile(filepath.Join(manager.Root, "trusted", publisher+".pub"), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0400); err != nil {
		return "", err
	}
	return digest(key), nil
}
func (manager Manager) Untrust(publisher string) error {
	if !namePattern.MatchString(publisher) {
		return fmt.Errorf("invalid publisher")
	}
	unlock, err := manager.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return os.Remove(filepath.Join(manager.Root, "trusted", publisher+".pub"))
}
func releasePath(root string, receipt Receipt) string {
	manifest := receipt.Manifest
	return filepath.Join(root, "releases", manifest.Name, manifest.Version+"-"+manifest.SHA256+"-"+receipt.PublisherFingerprint)
}
func (manager Manager) active(name string) (Receipt, error) {
	var receipt Receipt
	if !namePattern.MatchString(name) {
		return receipt, fmt.Errorf("invalid plugin name")
	}
	root, err := os.OpenRoot(filepath.Join(manager.Root, "active"))
	if err != nil {
		return receipt, err
	}
	defer root.Close()
	data, err := regular(root, name+".json", 8192)
	if err != nil {
		return receipt, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return receipt, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return receipt, fmt.Errorf("extra plugin receipt data")
	}
	if err := ValidateManifest(receipt.Manifest); err != nil {
		return receipt, err
	}
	if receipt.Manifest.Name != name || !digestPattern.MatchString(receipt.ManifestSHA256) || !digestPattern.MatchString(receipt.PublisherFingerprint) {
		return receipt, fmt.Errorf("invalid plugin receipt")
	}
	return receipt, nil
}
func (manager Manager) verify(receipt Receipt) (string, error) {
	manifest := receipt.Manifest
	path := releasePath(manager.Root, receipt)
	if err := privateDirectory(filepath.Dir(path), false); err != nil {
		return "", err
	}
	if err := privateDirectory(path, false); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	raw, err := regular(root, "manifest.json", 8192)
	if err != nil {
		return "", err
	}
	parsed, err := parseManifest(raw)
	if err != nil {
		return "", err
	}
	if parsed != manifest || digest(raw) != receipt.ManifestSHA256 {
		return "", fmt.Errorf("installed plugin manifest changed")
	}
	key, err := manager.readKey(manifest.Publisher)
	if err != nil {
		return "", err
	}
	if digest(key) != receipt.PublisherFingerprint {
		return "", fmt.Errorf("publisher key changed; reinstall with the newly trusted key")
	}
	signature, err := regular(root, "manifest.sig", 128)
	if err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || !ed25519.Verify(key, raw, decoded) {
		return "", fmt.Errorf("plugin signature verification failed")
	}
	binary, err := regular(root, "plugin", maxBinary)
	if err != nil {
		return "", err
	}
	if len(binary) != manifest.Bytes || digest(binary) != manifest.SHA256 {
		return "", fmt.Errorf("plugin binary hash or size changed")
	}
	info, err := root.Lstat("plugin")
	if err != nil || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return "", fmt.Errorf("plugin executable has unsafe ownership or permissions")
	}
	return filepath.Join(path, "plugin"), nil
}

func (manager Manager) Install(bundle string, allowDowngrade bool) (Receipt, error) {
	var receipt Receipt
	unlock, err := manager.lock()
	if err != nil {
		return receipt, err
	}
	defer unlock()
	source, err := os.OpenRoot(bundle)
	if err != nil {
		return receipt, err
	}
	defer source.Close()
	raw, err := regular(source, "manifest.json", 8192)
	if err != nil {
		return receipt, err
	}
	manifest, err := parseManifest(raw)
	if err != nil {
		return receipt, err
	}
	key, err := manager.readKey(manifest.Publisher)
	if err != nil {
		return receipt, err
	}
	signature, err := regular(source, "manifest.sig", 128)
	if err != nil {
		return receipt, err
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || !ed25519.Verify(key, raw, decoded) {
		return receipt, fmt.Errorf("plugin signature verification failed")
	}
	binary, err := regular(source, "plugin", maxBinary)
	if err != nil {
		return receipt, err
	}
	if len(binary) != manifest.Bytes || digest(binary) != manifest.SHA256 {
		return receipt, fmt.Errorf("plugin binary hash or size does not match its signed manifest")
	}
	previous, err := manager.active(manifest.Name)
	if err != nil && !os.IsNotExist(err) {
		return receipt, err
	}
	if err == nil {
		if previous.Manifest.Publisher != manifest.Publisher {
			return receipt, fmt.Errorf("plugin publisher cannot change during an update")
		}
		compared := versionCompare(manifest.Version, previous.Manifest.Version)
		if compared < 0 && !allowDowngrade {
			return receipt, fmt.Errorf("downgrade requires explicit --allow-downgrade")
		}
		if compared == 0 && (previous.Manifest.SHA256 != manifest.SHA256 || previous.ManifestSHA256 != digest(raw)) {
			return receipt, fmt.Errorf("an installed version cannot be replaced with different signed bytes")
		}
	}
	if previous.Manifest.Name == "" {
		entries, listErr := os.ReadDir(filepath.Join(manager.Root, "active"))
		if listErr != nil {
			return receipt, listErr
		}
		count := 0
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".previous.json") {
				count++
			}
		}
		if count >= 50 {
			return receipt, fmt.Errorf("plugin registry reached 50 active plugins")
		}
	}
	receipt = Receipt{Manifest: manifest, ManifestSHA256: digest(raw), PublisherFingerprint: digest(key)}
	parent := filepath.Join(manager.Root, "releases", manifest.Name)
	if _, err := os.Lstat(parent); os.IsNotExist(err) {
		entries, listErr := os.ReadDir(filepath.Join(manager.Root, "releases"))
		if listErr != nil {
			return receipt, listErr
		}
		if len(entries) >= 100 {
			return receipt, fmt.Errorf("retained plugin-name budget reached; remove and prune inactive plugins")
		}
	} else if err != nil {
		return receipt, err
	}
	var space syscall.Statfs_t
	if err := syscall.Statfs(manager.Root, &space); err != nil {
		return receipt, err
	}
	if uint64(space.Bavail)*uint64(space.Bsize) < uint64(manifest.Bytes)*2+64*1024*1024 {
		return receipt, fmt.Errorf("insufficient free space for a safe plugin install")
	}
	if err := privateDirectory(parent, true); err != nil {
		return receipt, err
	}
	destination := releasePath(manager.Root, receipt)
	if _, err := os.Lstat(destination); err == nil {
		if _, err := manager.verify(receipt); err != nil {
			return receipt, err
		}
	} else if !os.IsNotExist(err) {
		return receipt, err
	} else {
		entries, err := os.ReadDir(parent)
		if err != nil {
			return receipt, err
		}
		if len(entries) >= 10 {
			return receipt, fmt.Errorf("plugin retains ten releases; prune old releases before updating")
		}
		stage, err := os.MkdirTemp(parent, ".plugin-stage-")
		if err != nil {
			return receipt, err
		}
		defer os.RemoveAll(stage)
		for name, data := range map[string][]byte{"manifest.json": raw, "manifest.sig": signature, "plugin": binary} {
			mode := os.FileMode(0400)
			if name == "plugin" {
				mode = 0500
			}
			if err := os.WriteFile(filepath.Join(stage, name), data, mode); err != nil {
				return receipt, err
			}
		}
		marker, err := json.Marshal(receipt)
		if err != nil {
			return receipt, err
		}
		if err := os.WriteFile(filepath.Join(stage, ".trace-plugin-release"), marker, 0400); err != nil {
			return receipt, err
		}
		if err := os.Rename(stage, destination); err != nil {
			return receipt, err
		}
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	active := filepath.Join(manager.Root, "active", manifest.Name+".json")
	if previous.Manifest.Name != "" {
		old, _ := json.Marshal(previous)
		if err := atomicFile(filepath.Join(manager.Root, "active", manifest.Name+".previous.json"), old, 0600); err != nil {
			return receipt, err
		}
	}
	if err := atomicFile(active, data, 0600); err != nil {
		return receipt, err
	}
	return receipt, nil
}
func (manager Manager) Executable(name string) (string, error) {
	if err := manager.check(false); err != nil {
		return "", err
	}
	receipt, err := manager.active(name)
	if err != nil {
		return "", err
	}
	return manager.verify(receipt)
}

type Installed struct {
	Receipt  Receipt `json:"receipt"`
	Verified bool    `json:"verified"`
	Error    string  `json:"error,omitempty"`
}

func (manager Manager) List() ([]Installed, error) {
	result := []Installed{}
	if err := manager.check(false); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(manager.Root, "active"))
	if err != nil {
		return nil, err
	}
	if len(entries) > 100 {
		return nil, fmt.Errorf("plugin registry exceeds budget")
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".previous.json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		receipt, err := manager.active(name)
		if err != nil {
			return nil, err
		}
		_, verifyErr := manager.verify(receipt)
		item := Installed{Receipt: receipt, Verified: verifyErr == nil}
		if verifyErr != nil {
			item.Error = "signature, publisher trust, binary integrity or private ownership check failed"
		}
		result = append(result, item)
	}
	return result, nil
}
func (manager Manager) Remove(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid plugin name")
	}
	unlock, err := manager.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := manager.active(name); err != nil {
		return err
	}
	return os.Remove(filepath.Join(manager.Root, "active", name+".json"))
}

// Prune removes only complete, marked release directories, retaining the active
// and previous receipts. Unexpected files or directories are never adopted.
func (manager Manager) Prune(name string) (int, error) {
	if !namePattern.MatchString(name) {
		return 0, fmt.Errorf("invalid plugin name")
	}
	unlock, err := manager.lock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	keep := map[string]bool{}
	hasActive := false
	if receipt, err := manager.active(name); err == nil {
		keep[releasePath(manager.Root, receipt)] = true
		hasActive = true
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	activeRoot, err := os.OpenRoot(filepath.Join(manager.Root, "active"))
	if err != nil {
		return 0, err
	}
	defer activeRoot.Close()
	if data, err := regular(activeRoot, name+".previous.json", 8192); err == nil {
		var receipt Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return 0, err
		}
		if err := ValidateManifest(receipt.Manifest); err != nil {
			return 0, err
		}
		if receipt.Manifest.Name != name || !digestPattern.MatchString(receipt.PublisherFingerprint) {
			return 0, fmt.Errorf("invalid previous receipt")
		}
		if hasActive {
			keep[releasePath(manager.Root, receipt)] = true
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	parent := filepath.Join(manager.Root, "releases", name)
	if err := privateDirectory(parent, false); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return 0, err
	}
	if len(entries) > 20 {
		return 0, fmt.Errorf("release directory exceeds pruning budget")
	}
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if keep[path] {
			continue
		}
		if err := privateDirectory(path, false); err != nil {
			return removed, err
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return removed, err
		}
		data, err := regular(root, ".trace-plugin-release", 8192)
		root.Close()
		if err != nil {
			return removed, fmt.Errorf("unrecognized release directory was preserved")
		}
		var receipt Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return removed, err
		}
		if err := ValidateManifest(receipt.Manifest); err != nil {
			return removed, err
		}
		if receipt.Manifest.Name != name || !digestPattern.MatchString(receipt.PublisherFingerprint) || releasePath(manager.Root, receipt) != path {
			return removed, fmt.Errorf("unrecognized release directory was preserved")
		}
		files, err := os.ReadDir(path)
		if err != nil {
			return removed, err
		}
		if len(files) != 4 {
			return removed, fmt.Errorf("unexpected files were preserved")
		}
		expected := map[string]bool{"manifest.json": true, "manifest.sig": true, "plugin": true, ".trace-plugin-release": true}
		for _, file := range files {
			info, err := file.Info()
			if err != nil || !expected[file.Name()] || !info.Mode().IsRegular() || !owned(info) {
				return removed, fmt.Errorf("unexpected release content was preserved")
			}
		}
		for _, file := range files {
			if err := os.Remove(filepath.Join(path, file.Name())); err != nil {
				return removed, err
			}
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	if !hasActive {
		if err := os.Remove(filepath.Join(manager.Root, "active", name+".previous.json")); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		if err := os.Remove(parent); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func DecodePublicKey(data []byte) (ed25519.PublicKey, error) {
	if len(data) > 1024 {
		return nil, fmt.Errorf("public key exceeds budget")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("key must be a single PEM PUBLIC KEY, never a private key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("publisher key must use Ed25519")
	}
	return key, nil
}
func PublicFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])
}
