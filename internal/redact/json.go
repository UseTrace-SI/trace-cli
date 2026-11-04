package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

var jsonCredentialField = regexp.MustCompile(`(?i)(?:^|[_-])(?:api[_-]?key|access[_-]?key|private[_-]?key|master[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|key|secret|token|password|passwd|authorization)$`)

// JSON redacts string values before serializing them again. Replacing text in
// serialized JSON directly can corrupt escapes or leave nested credentials.
func (r Redactor) JSON(data []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("JSON redaction requires exactly one value")
	}
	var clean func(any, int) (any, error)
	clean = func(value any, depth int) (any, error) {
		if depth > 128 {
			return nil, fmt.Errorf("JSON redaction nesting exceeds 128 levels")
		}
		switch v := value.(type) {
		case string:
			return r.Text(v), nil
		case []any:
			for i, item := range v {
				next, err := clean(item, depth+1)
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
		case map[string]any:
			for key, item := range v {
				if text, ok := item.(string); ok && text != "" && jsonCredentialField.MatchString(key) {
					if match := redactionMarker.FindStringIndex(text); match == nil || match[0] != 0 || match[1] != len(text) {
						v[key] = marker("env", text)
						continue
					}
				}
				next, err := clean(item, depth+1)
				if err != nil {
					return nil, err
				}
				v[key] = next
			}
		}
		return value, nil
	}
	cleaned, err := clean(value, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cleaned)
}
