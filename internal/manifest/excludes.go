package manifest

import (
	"path"
	"strings"
)

// secretNames are file names that hold credentials wherever they appear.
var secretNames = map[string]bool{
	".npmrc":           true, // npm registry auth tokens
	".pypirc":          true, // PyPI upload credentials
	".netrc":           true,
	"_netrc":           true,
	".git-credentials": true,
	".pgpass":          true,
	"credentials.json": true, // Google OAuth clients and service accounts
	"kubeconfig":       true,
}

// secretGlobs match a single path element.
var secretGlobs = []string{
	"*.pem", "*.key",
	"*.p12", "*.pfx", "*.jks", "*.keystore", // certificate and key stores
	"service-account*.json",         // cloud service account keys
	"*.tfstate", "*.tfstate.backup", // Terraform state holds resource secrets
	"kubeconfig.yaml", "kubeconfig.yml", "*.kubeconfig",
}

// secretFiles are files identified by their directory as well as their name.
var secretFiles = []string{".kube/config", ".docker/config.json"}

func SecretPath(rel string) bool {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	// Fold case as excluded does: Server.PEM is as secret as server.pem, and
	// case-insensitive file systems resolve .SSH to .ssh.
	folded := foldPath(rel)
	for _, file := range secretFiles {
		if folded == file || strings.HasSuffix(folded, "/"+file) {
			return true
		}
	}
	for _, part := range strings.Split(folded, "/") {
		if part == ".aws" || part == ".ssh" || strings.HasPrefix(part, ".env") || secretNames[part] {
			return true
		}
		// Default OpenSSH key names for every key type, with their .pub files.
		for _, key := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
			if strings.HasPrefix(part, key) {
				return true
			}
		}
		for _, glob := range secretGlobs {
			if ok, _ := path.Match(glob, part); ok {
				return true
			}
		}
	}
	return false
}
