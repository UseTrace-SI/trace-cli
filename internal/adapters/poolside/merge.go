package poolside

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

var plainKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func eventBlock(name string, existing bool) string {
	mode := "new"
	if existing {
		mode = "existing"
	}
	command, _ := json.Marshal(hookcmd.Command("poolside", name))
	var b strings.Builder
	fmt.Fprintf(&b, "  # BEGIN TRACE POOLSIDE %s %s\n", name, mode)
	if !existing {
		fmt.Fprintf(&b, "  %s:\n", name)
	}
	fmt.Fprintf(&b, "    - name: trace-%s\n      matcher: \"*\"\n      command: %s\n", strings.ToLower(name), command)
	fmt.Fprintf(&b, "  # END TRACE POOLSIDE %s\n", name)
	return b.String()
}

func managedEventBlock(actual, expected, name string) bool {
	a, b := strings.Split(actual, "\n"), strings.Split(expected, "\n")
	if len(a) != len(b) {
		return false
	}
	for i := range b {
		const prefix = "      command: "
		if strings.HasPrefix(b[i], prefix) {
			var command string
			if !strings.HasPrefix(a[i], prefix) || json.Unmarshal([]byte(strings.TrimPrefix(a[i], prefix)), &command) != nil || !hookcmd.IsTraceHook(command, "poolside") || !hookcmd.IsTraceHookFor(command, "poolside", name) {
				return false
			}
		} else if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stripEventBlocks removes only exact Trace-owned fragments. If a user added
// hooks to an event created by Trace, retain its key and the new list entries.
func stripEventBlocks(text string) (string, bool, error) {
	found := false
	for _, name := range names {
		for _, existing := range []bool{false, true} {
			expected := eventBlock(name, existing)
			begin := strings.SplitN(expected, "\n", 2)[0] + "\n"
			end := "  # END TRACE POOLSIDE " + name + "\n"
			start := strings.Index(text, begin)
			if start < 0 {
				continue
			}
			if strings.Count(text, begin) != 1 {
				return "", false, fmt.Errorf("duplicate Trace Poolside hook block")
			}
			finish := strings.Index(text[start:], end)
			if finish < 0 {
				return "", false, fmt.Errorf("incomplete Trace Poolside hook block")
			}
			finish += start + len(end)
			if !managedEventBlock(text[start:finish], expected, name) {
				return "", false, fmt.Errorf("trace Poolside hook block was modified")
			}
			replacement := ""
			if !existing {
				for _, line := range strings.Split(text[finish:], "\n") {
					trimmed := strings.TrimSpace(line)
					if trimmed == "" || strings.HasPrefix(trimmed, "#") {
						continue
					}
					if strings.HasPrefix(line, "    - ") || strings.TrimSpace(line) == "-" && strings.HasPrefix(line, "    ") {
						replacement = "  " + name + ":\n"
					}
					break
				}
			}
			text = text[:start] + replacement + text[finish:]
			found = true
		}
	}
	if strings.Contains(text, "# BEGIN TRACE POOLSIDE ") || strings.Contains(text, "# END TRACE POOLSIDE ") {
		return "", false, fmt.Errorf("invalid Trace Poolside hook blocks")
	}
	return text, found, nil
}

// mergeExistingHooks handles ordinary two-space block mappings and block
// sequences. Other YAML is rejected rather than risking a destructive rewrite.
func mergeExistingHooks(text string, remove bool) (string, bool, error) {
	clean, found, err := stripEventBlocks(text)
	if err != nil {
		return "", false, err
	}
	if remove {
		return clean, found, nil
	}
	if strings.Contains(clean, "\r") || strings.Contains(clean, "\t") {
		return "", false, fmt.Errorf("poolside settings need manual YAML merging")
	}
	lines := strings.SplitAfter(clean, "\n")
	hooksStart, hooksEnd, offset := -1, len(clean), 0
	inHooks := false
	positions := map[string]int{}
	awaitingList := ""
	for _, line := range lines {
		body := strings.TrimSuffix(line, "\n")
		trim := strings.TrimSpace(body)
		if trim == "" || strings.HasPrefix(trim, "#") {
			offset += len(line)
			continue
		}
		if !strings.HasPrefix(body, " ") {
			if inHooks {
				hooksEnd = offset
				inHooks = false
				awaitingList = ""
			}
			i := strings.IndexByte(body, ':')
			if i <= 0 || !plainKey.MatchString(body[:i]) {
				return "", false, fmt.Errorf("poolside settings need manual YAML merging")
			}
			if body[:i] == "hooks" {
				if hooksStart >= 0 || (strings.TrimSpace(body[i+1:]) != "" && !strings.HasPrefix(strings.TrimSpace(body[i+1:]), "#")) || !strings.HasSuffix(line, "\n") {
					return "", false, fmt.Errorf("poolside hooks need manual YAML merging")
				}
				hooksStart, inHooks = offset+len(line), true
			}
		} else if inHooks && strings.HasPrefix(body, "  ") && !strings.HasPrefix(body, "   ") {
			awaitingList = ""
			entry := body[2:]
			i := strings.IndexByte(entry, ':')
			if i <= 0 || !plainKey.MatchString(entry[:i]) {
				return "", false, fmt.Errorf("poolside hooks need manual YAML merging")
			}
			name := entry[:i]
			if _, exists := positions[name]; exists {
				return "", false, fmt.Errorf("duplicate Poolside hook event")
			}
			positions[name] = offset + len(line)
			for _, target := range names {
				if name == target {
					if (strings.TrimSpace(entry[i+1:]) != "" && !strings.HasPrefix(strings.TrimSpace(entry[i+1:]), "#")) || !strings.HasSuffix(line, "\n") {
						return "", false, fmt.Errorf("poolside hook %s needs manual YAML merging", name)
					}
					awaitingList = name
				}
			}
		} else if inHooks {
			if !strings.HasPrefix(body, "    ") {
				return "", false, fmt.Errorf("poolside hooks need manual YAML merging")
			}
			if awaitingList != "" {
				if !strings.HasPrefix(body, "    - ") && body != "    -" {
					return "", false, fmt.Errorf("poolside hook %s needs manual YAML merging", awaitingList)
				}
				awaitingList = ""
			}
		}
		offset += len(line)
	}
	if hooksStart < 0 {
		return text, found, nil
	}
	if strings.Contains(clean[hooksStart:hooksEnd], "trace-") {
		return "", false, fmt.Errorf("poolside hook name conflicts with Trace")
	}
	insert := map[int]string{}
	for _, name := range names {
		pos, exists := positions[name]
		if !exists {
			pos = hooksStart
		}
		insert[pos] += eventBlock(name, exists)
	}
	var result strings.Builder
	for i := 0; i <= len(clean); i++ {
		result.WriteString(insert[i])
		if i < len(clean) {
			result.WriteByte(clean[i])
		}
	}
	return result.String(), true, nil
}
