package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// maxStateFileBytes bounds marker reads; markers hold a PID or start time.
const maxStateFileBytes = 64

// CheckTraceDir refuses a state directory that the repository controls. Git
// materialises tracked .trace/ entries, including symlinks such as
// daemon.pid -> ../../victim, and the daemon writes to these paths. It runs
// Git, so call it once when the daemon starts, never on the hook path.
func CheckTraceDir(root string) error {
	dir := filepath.Join(root, ".trace")
	if err := checkStateDir(dir); err != nil {
		return err
	}
	// On a case-insensitive file system .TRACE/ is the same directory.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", ":(icase).trace").Output()
	if err != nil {
		return fmt.Errorf("checking whether Git tracks .trace: %w", err)
	}
	if len(out) != 0 {
		return fmt.Errorf("refusing to use %s: Git tracks files under it; Trace state must stay local", dir)
	}
	return nil
}

// checkStateDir accepts a missing directory or a real one, never a symlink.
func checkStateDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	return nil
}

// ensureStateDir creates dir if needed and requires a real directory, so
// state is never written into a directory a symlink points at.
func ensureStateDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	return nil
}

// openStateFile opens a state file without following a symlink in its place
// and requires a regular file.
func openStateFile(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, 0600)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("refusing to follow symlink %s", path)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// readStateFile reads a small marker without following a symlink, so a
// repository can neither choose its contents nor stall a hook by pointing it
// at an endless device.
func readStateFile(path string) ([]byte, error) {
	f, err := openStateFile(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxStateFileBytes))
}

// WriteStateFile replaces a small state file by renaming a new file over it,
// which replaces a symlink or hard link planted at path instead of writing
// through it. The directory must already be a real state directory.
func WriteStateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
