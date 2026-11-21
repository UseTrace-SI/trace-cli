package manifest

import (
	"path"
	"strings"
)

func SecretPath(rel string) bool {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	// Fold case as excluded does: Server.PEM is as secret as server.pem, and
	// case-insensitive file systems resolve .SSH to .ssh.
	for _, part := range strings.Split(foldPath(rel), "/") {
		if part == ".aws" || part == ".ssh" || strings.HasPrefix(part, ".env") {
			return true
		}
		// Default OpenSSH key names for every key type, with their .pub files.
		for _, key := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
			if strings.HasPrefix(part, key) {
				return true
			}
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
