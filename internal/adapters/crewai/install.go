package crewai

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

//go:embed bridge.py
var bridge string

const relative = ".trace/integrations/trace_crewai.py"

func Install(root string) error {
	p := filepath.Join(root, relative)
	data := strings.ReplaceAll(bridge, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	data = strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
	if b, err := os.ReadFile(p); err == nil && string(b) != data {
		return fmt.Errorf("refusing to replace modified CrewAI bridge at %s", p)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading CrewAI bridge: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating CrewAI integration directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		return fmt.Errorf("writing CrewAI bridge: %w", err)
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
		return fmt.Errorf("reading CrewAI bridge: %w", err)
	}
	data := strings.ReplaceAll(bridge, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	data = strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
	if string(b) != data {
		return fmt.Errorf("refusing to remove modified CrewAI bridge at %s", p)
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("removing CrewAI bridge: %w", err)
	}
	return nil
}
