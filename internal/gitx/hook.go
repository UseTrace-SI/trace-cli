package gitx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

const begin = "# trace post-commit begin\n"
const end = "# trace post-commit end\n"

// hookBlock renders the Trace-managed part of the post-commit hook. The
// executable is absolute so GUI Git clients with a minimal PATH still record
// commits.
func hookBlock() string {
	var b strings.Builder
	b.WriteString(begin)
	b.WriteString("sha=$(git rev-parse HEAD 2>/dev/null) || exit 0\n")
	b.WriteString("printf '{\"git_sha\":\"%s\"}\\n' \"$sha\" | ")
	b.WriteString(hookcmd.Command("git", "git_commit"))
	b.WriteString(" >/dev/null 2>&1 || true\n")
	b.WriteString(end)
	return b.String()
}

func hookPaths(root string) (string, string, string, error) {
	p, err := GitPath(root, "hooks/post-commit")
	if err != nil {
		return "", "", "", err
	}
	return p, filepath.Join(root, ".trace", "post-commit-original"), filepath.Join(root, ".trace", "post-commit-installed-sha"), nil
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func InstallHook(root string) error {
	p, record, installedHash, err := hookPaths(root)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	existed := err == nil
	if start := strings.Index(string(current), begin); start >= 0 {
		stop := strings.Index(string(current), end)
		if stop < start {
			return nil
		}
		stop += len(end)
		// Rewrite only the Trace block so hooks installed by older versions
		// pick up the absolute executable path.
		updated := append(append(append([]byte{}, current[:start]...), hookBlock()...), current[stop:]...)
		if string(updated) == string(current) {
			return nil
		}
		if err := os.WriteFile(p, updated, 0755); err != nil {
			return fmt.Errorf("updating post-commit hook: %w", err)
		}
		return os.WriteFile(installedHash, []byte(hash(updated)), 0600)
	}
	backup := ""
	if existed {
		backup = p + ".pre-trace"
		if _, err := os.Stat(backup); err == nil {
			return fmt.Errorf("existing post-commit backup blocks Trace installation: %s", backup)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(p, backup); err != nil {
			return fmt.Errorf("preserving post-commit hook: %w", err)
		}
	}
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	if backup != "" {
		script.WriteString("if [ -x \"$0.pre-trace\" ]; then \"$0.pre-trace\" \"$@\"; fi\n")
	}
	script.WriteString(hookBlock())
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b := []byte(script.String())
	if err := os.WriteFile(p, b, 0755); err != nil {
		return fmt.Errorf("writing post-commit hook: %w", err)
	}
	if err := os.WriteFile(record, []byte(backup), 0600); err != nil {
		return err
	}
	return os.WriteFile(installedHash, []byte(hash(b)), 0600)
}

func UninstallHook(root string) error {
	p, record, installedHash, err := hookPaths(root)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(current), begin) {
		return nil
	}
	backupBytes, _ := os.ReadFile(record)
	backup := string(backupBytes)
	hashBytes, _ := os.ReadFile(installedHash)
	if hash(current) == string(hashBytes) {
		if err := os.Remove(p); err != nil {
			return err
		}
		if backup != "" {
			return os.Rename(backup, p)
		}
		return nil
	}
	start := strings.Index(string(current), begin)
	stop := strings.Index(string(current), end)
	if start < 0 || stop < start {
		return nil
	}
	stop += len(end)
	cleaned := append(append([]byte{}, current[:start]...), current[stop:]...)
	return os.WriteFile(p, cleaned, 0755)
}
