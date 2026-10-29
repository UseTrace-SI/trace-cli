package aider

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

const relative = ".trace/integrations/trace_aider.py"

func generated(root string) string {
	data := strings.ReplaceAll(bridge, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	return strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
}

func responseBridgeGenerated(root string) string {
	current := generated(root)
	updated := `        if not isinstance(message, str) or not message:
            return self.coder.run(*args, **kwargs)
        stripped = message.lstrip()
        preproc = args[1] if len(args) > 1 else kwargs.get("preproc", True)
        if preproc and stripped.startswith(("/", "!")):
            parts = stripped.split(None, 1)
            if len(parts) != 2 or parts[0] not in ("/ask", "/code", "/architect", "/context") or not parts[1].strip():
                return self.coder.run(*args, **kwargs)
`
	previous := `        if not isinstance(message, str) or not message or message.lstrip().startswith(("/", "!")):
            return self.coder.run(*args, **kwargs)
`
	return strings.Replace(current, updated, previous, 1)
}

func previousGenerated(root string) string {
	current := responseBridgeGenerated(root)
	updated := `            response = None
            try:
                response = self.coder.run(*args, **kwargs)
                return response
            finally:
                if recorded:
                    # Aider can change files without reporting each path.
                    batch = [("tool_edit", payload)]
                    if isinstance(response, str) and response and len(response) <= 65536:
                        batch.append(("turn_context", {**payload, "context_type": "response", "text": response}))
                    batch.append(("turn_end", payload))
                    self._send(batch)
`
	previous := `            try:
                return self.coder.run(*args, **kwargs)
            finally:
                if recorded:
                    # Aider can change files without reporting each path.
                    self._send([("tool_edit", payload), ("turn_end", payload)])
`
	return strings.Replace(current, updated, previous, 1)
}

// legacyGenerated recognizes the previous managed bridge exactly, so an
// existing installation can be upgraded without replacing user edits.
func legacyGenerated(root string) string {
	current := previousGenerated(root)
	header := `    def run(self, *args, **kwargs):
        # Keep the bridge's original message= spelling as an alias while also
        # accepting Aider's native with_message= calling convention.
        if not args and "message" in kwargs and "with_message" not in kwargs:
            kwargs = dict(kwargs)
            args = (kwargs.pop("message"),)
        message = args[0] if args else kwargs.get("with_message")
`
	current = strings.Replace(current, header, "    def run(self, message, *args, **kwargs):\n", 1)
	return strings.ReplaceAll(current, "return self.coder.run(*args, **kwargs)", "return self.coder.run(message, *args, **kwargs)")
}

func Install(root string) error {
	p := filepath.Join(root, relative)
	data := generated(root)
	if b, err := os.ReadFile(p); err == nil && string(b) != data && string(b) != responseBridgeGenerated(root) && string(b) != previousGenerated(root) && string(b) != legacyGenerated(root) {
		return fmt.Errorf("refusing to replace modified Aider bridge at %s", p)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Aider bridge: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Aider integration directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		return fmt.Errorf("writing Aider bridge: %w", err)
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
		return fmt.Errorf("reading Aider bridge: %w", err)
	}
	if string(b) != generated(root) && string(b) != responseBridgeGenerated(root) && string(b) != previousGenerated(root) && string(b) != legacyGenerated(root) {
		return fmt.Errorf("refusing to remove modified Aider bridge at %s", p)
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("removing Aider bridge: %w", err)
	}
	return nil
}
