package manifest

import (
	"strings"
)

// secretNames are file names that hold credentials. They match a file's own
// name only, so a directory such as pkg/kubeconfig/ is not excluded.
var secretNames = map[string]bool{
	".npmrc":           true, // npm registry auth tokens
	".pypirc":          true, // PyPI upload credentials
	".netrc":           true,
	"_netrc":           true,
	".git-credentials": true,
	".pgpass":          true,
	"credentials.json": true, // Google OAuth clients and service accounts
	"kubeconfig":       true,
	"kubeconfig.yaml":  true,
	"kubeconfig.yml":   true,
}

// secretSuffixes match the end of a file's own name.
var secretSuffixes = []string{
	".p12", ".pfx", ".jks", ".keystore", // certificate and key stores
	".tfstate", ".tfstate.backup", // Terraform state holds resource secrets
	".kubeconfig",
}

// secretFiles are files identified by their directory as well as their name.
var secretFiles = []string{".kube/config", ".docker/config.json"}

// sshKeyPrefixes are the default OpenSSH key names for every key type, with
// their .pub files.
var sshKeyPrefixes = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

func SecretPath(rel string) bool {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	// Fold case as excluded does: Server.PEM is as secret as server.pem, and
	// case-insensitive file systems resolve .SSH to .ssh.
	return secretFolded(foldPath(rel))
}

// secretFolded is SecretPath for a path foldPath already normalized. Its
// checks are plain string comparisons: it runs for every path of a scan.
func secretFolded(folded string) bool {
	// Every path element: credential directories, .env files and directories,
	// SSH keys and PEM/key files, as before.
	rest := folded
	for {
		part, next, more := strings.Cut(rest, "/")
		if part == ".aws" || part == ".ssh" || strings.HasPrefix(part, ".env") ||
			strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return true
		}
		for _, key := range sshKeyPrefixes {
			if strings.HasPrefix(part, key) {
				return true
			}
		}
		if !more {
			break
		}
		rest = next
	}
	// The file's own name.
	base := folded[strings.LastIndexByte(folded, '/')+1:]
	if secretNames[base] {
		return true
	}
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	if strings.HasPrefix(base, "service-account") && strings.HasSuffix(base, ".json") {
		return true
	}
	for _, file := range secretFiles {
		if folded == file || strings.HasSuffix(folded, "/"+file) {
			return true
		}
	}
	return false
}
