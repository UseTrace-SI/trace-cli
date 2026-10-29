package codebuff

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

//go:embed bridge.mjs
var bridge string

const relative = ".trace/integrations/trace_codebuff.mjs"

func generated(root string) string {
	data := strings.ReplaceAll(bridge, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	return strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
}

func Install(root string) error {
	p := filepath.Join(root, relative)
	data := generated(root)
	if b, err := os.ReadFile(p); err == nil && string(b) != data {
		return fmt.Errorf("refusing to replace modified Codebuff bridge at %s", p)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Codebuff bridge: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Codebuff integration directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		return fmt.Errorf("writing Codebuff bridge: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	p := filepath.Join(root, relative)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Codebuff bridge: %w", err)
	}
	if string(b) != generated(root) {
		return fmt.Errorf("refusing to remove modified Codebuff bridge at %s", p)
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("removing Codebuff bridge: %w", err)
	}
	return nil
}
