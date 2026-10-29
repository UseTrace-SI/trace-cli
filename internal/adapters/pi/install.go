package pi

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

//go:embed extension.ts
var extension string

//go:embed global.ts
var globalExtension string

//go:embed global_v1.ts
var previousGlobalExtension string

const relative = ".pi/extensions/trace/index.ts"
const marker = "Trace coding-agent extension. Managed by trace agent add/remove."

var ompProfileName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var ompReservedName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$`)

func globalAgentDir(home, agent string) (string, error) {
	if agent != "pi" && agent != "omp" {
		return "", fmt.Errorf("unsupported agent %q", agent)
	}
	if agent == "omp" {
		profile, present := os.LookupEnv("OMP_PROFILE")
		if !present {
			profile = os.Getenv("PI_PROFILE")
		}
		profile = strings.TrimSpace(profile)
		if profile == "default" {
			profile = ""
		}
		config := os.Getenv("PI_CONFIG_DIR")
		if config == "" {
			config = ".omp"
		}
		if profile != "" {
			if !ompProfileName.MatchString(profile) || strings.HasSuffix(profile, ".") || ompReservedName.MatchString(profile) {
				return "", fmt.Errorf("invalid Oh My Pi profile %q", profile)
			}
			return filepath.Join(home, config, "profiles", profile, "agent"), nil
		}
		if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
			if !filepath.IsAbs(dir) {
				return "", fmt.Errorf("agent directory must be absolute: %s", dir)
			}
			return dir, nil
		}
		return filepath.Join(home, config, "agent"), nil
	}
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".pi", "agent")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("agent directory must be absolute: %s", dir)
	}
	return dir, nil
}

func renderGlobal(agent, binary string) []byte {
	return renderGlobalVersion(globalExtension, agent, binary)
}

func renderGlobalVersion(source, agent, binary string) []byte {
	data := strings.ReplaceAll(source, "__TRACE_BIN__", strconv.Quote(binary))
	return []byte(strings.ReplaceAll(data, "__TRACE_AGENT__", strconv.Quote(agent)))
}

// SetupGlobal installs or removes only an unmodified Trace-owned extension.
func SetupGlobal(home, agent string, remove bool) error {
	dir, err := globalAgentDir(home, agent)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "extensions", "trace-global", "index.ts")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular extension at %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading global extension: %w", err)
		}
		const prefix = "const traceBin = "
		start := strings.Index(string(data), prefix)
		if start < 0 {
			return fmt.Errorf("refusing to replace user extension at %s", path)
		}
		literal := strings.SplitN(string(data)[start+len(prefix):], ";\n", 2)[0]
		binary, parseErr := strconv.Unquote(literal)
		if parseErr != nil || (string(data) != string(renderGlobal(agent, binary)) && string(data) != string(renderGlobalVersion(previousGlobalExtension, agent, binary))) {
			return fmt.Errorf("refusing to replace user extension at %s", path)
		}
		if remove {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing global extension: %w", err)
			}
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking global extension: %w", err)
	} else if remove {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating global extension directory: %w", err)
	}
	if err := os.WriteFile(path, renderGlobal(agent, hookcmd.Path()), 0600); err != nil {
		return fmt.Errorf("writing global extension: %w", err)
	}
	return nil
}

// Render produces the shared Pi/Oh My Pi extension with repo-specific routing.
func Render(root, agent string) []byte {
	data := strings.ReplaceAll(extension, "__TRACE_BIN__", strconv.Quote(hookcmd.Path()))
	data = strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root))
	data = strings.ReplaceAll(data, "__TRACE_AGENT__", strconv.Quote(agent))
	return []byte(data)
}

func IsManaged(data []byte) bool {
	return strings.Contains(string(data), marker) || strings.Contains(string(data), "Trace Pi extension. Managed by trace agent add/remove pi.")
}

func Install(root string) error {
	path := filepath.Join(root, relative)
	if existing, err := os.ReadFile(path); err == nil && !IsManaged(existing) {
		return fmt.Errorf("refusing to replace user Pi extension at %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Pi extension: %w", err)
	}
	data := Render(root, "pi")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating Pi extension directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing Pi extension: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	path := filepath.Join(root, relative)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Pi extension: %w", err)
	}
	if !IsManaged(b) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing Pi extension: %w", err)
	}
	return nil
}
