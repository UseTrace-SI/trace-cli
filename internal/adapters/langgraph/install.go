package langgraph

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

const relative = ".trace/integrations/trace_langgraph.py"
const marker = "Trace LangGraph bridge. Managed by trace agent add/remove."

func Install(root string) error {
	p := filepath.Join(root, relative)
	if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), marker) {
		return fmt.Errorf("refusing to replace user LangGraph bridge at %s", p)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading LangGraph bridge: %w", err)
	}
	data := strings.ReplaceAll(bridge, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	data = strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating integrations directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		return fmt.Errorf("writing LangGraph bridge: %w", err)
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
		return fmt.Errorf("reading LangGraph bridge: %w", err)
	}
	if strings.Contains(string(b), marker) {
		return os.Remove(p)
	}
	return nil
}
