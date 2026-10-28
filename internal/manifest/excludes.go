package manifest

import (
	"path"
	"strings"
)

func SecretPath(rel string) bool {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	for _, part := range strings.Split(rel, "/") {
		if part == ".aws" || part == ".ssh" || strings.HasPrefix(part, ".env") || strings.HasPrefix(part, "id_rsa") {
			return true
		}
		if ok, _ := path.Match("*.pem", part); ok {
			return true
		}
		if ok, _ := path.Match("*.key", part); ok {
			return true
		}
	}
	return false
}
