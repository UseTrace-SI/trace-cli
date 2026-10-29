// Package zoo builds the optional VS Code observer for Zoo Code.
package zoo

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

//go:embed extension.js
var extension string

//go:embed extension_legacy.js
var legacyExtension string

const relative = ".trace/integrations/trace-zoo.vsix"
const packageJSON = `{"name":"trace-zoo","displayName":"Trace Zoo Code Observer","publisher":"usetrace-si","version":"0.0.1","engines":{"vscode":"^1.100.0"},"main":"./extension.js","activationEvents":["onStartupFinished"],"extensionDependencies":["ZooCodeOrganization.zoo-code"]}`
const contentTypes = `<?xml version="1.0" encoding="utf-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="json" ContentType="application/json"/><Default Extension="js" ContentType="application/javascript"/><Default Extension="vsixmanifest" ContentType="text/xml"/></Types>`
const legacyManifest = `<?xml version="1.0" encoding="utf-8"?><PackageManifest Version="2.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011"><Metadata><Identity Language="en-US" Id="trace-zoo" Version="0.0.1" Publisher="usetrace-si"/><DisplayName>Trace Zoo Code Observer</DisplayName><Description xml:space="preserve">Local task observer for Trace</Description></Metadata><Installation><InstallationTarget Id="Microsoft.VisualStudio.Code" Version="[1.100.0,)"/></Installation><Dependencies/><Assets><Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" Addressable="true"/></Assets></PackageManifest>`
const manifest = `<?xml version="1.0" encoding="utf-8"?><PackageManifest Version="2.0.0" xmlns="http://schemas.microsoft.com/developer/vsx-schema/2011"><Metadata><Identity Language="en-US" Id="trace-zoo" Version="0.0.1" Publisher="usetrace-si"/><DisplayName>Trace Zoo Code Observer</DisplayName><Description xml:space="preserve">Local task observer for Trace</Description><Properties><Property Id="Microsoft.VisualStudio.Code.Engine" Value="^1.100.0"/><Property Id="Microsoft.VisualStudio.Code.ExtensionDependencies" Value="ZooCodeOrganization.zoo-code"/><Property Id="Microsoft.VisualStudio.Code.ExecutesCode" Value="true"/></Properties></Metadata><Installation><InstallationTarget Id="Microsoft.VisualStudio.Code"/></Installation><Dependencies/><Assets><Asset Type="Microsoft.VisualStudio.Code.Manifest" Path="extension/package.json" Addressable="true"/></Assets></PackageManifest>`

func Path(home string) string { return filepath.Join(home, relative) }

func archive(binary string) ([]byte, error) {
	return archiveWithExtension(binary, extension)
}

func archiveWithExtension(binary, source string) ([]byte, error) {
	quoted, err := json.Marshal(binary)
	if err != nil {
		return nil, fmt.Errorf("encoding Trace executable path: %w", err)
	}
	var output bytes.Buffer
	z := zip.NewWriter(&output)
	packageManifest := manifest
	if source == legacyExtension {
		packageManifest = legacyManifest
	}
	for _, file := range []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"extension.vsixmanifest", packageManifest},
		{"extension/package.json", packageJSON},
		{"extension/extension.js", strings.ReplaceAll(source, "__TRACE_BIN__", string(quoted))},
	} {
		w, err := z.Create(file.name)
		if err != nil {
			return nil, fmt.Errorf("creating Zoo extension archive: %w", err)
		}
		if _, err := w.Write([]byte(file.body)); err != nil {
			return nil, fmt.Errorf("writing Zoo extension archive: %w", err)
		}
	}
	if err := z.Close(); err != nil {
		return nil, fmt.Errorf("closing Zoo extension archive: %w", err)
	}
	return output.Bytes(), nil
}

// managedArchive recognizes the same generated package with an older Trace
// executable path, so upgrading a binary does not strand the installation.
func managedArchive(data []byte) bool {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, file := range z.File {
		if file.Name != "extension/extension.js" || file.UncompressedSize64 > 1<<20 {
			continue
		}
		r, err := file.Open()
		if err != nil {
			return false
		}
		body, err := io.ReadAll(io.LimitReader(r, 1<<20))
		_ = r.Close()
		if err != nil {
			return false
		}
		const prefix = "const TRACE = "
		start := strings.Index(string(body), prefix)
		if start < 0 {
			return false
		}
		value := string(body)[start+len(prefix):]
		end := strings.Index(value, ";\n")
		if end < 0 {
			return false
		}
		var previous string
		if json.Unmarshal([]byte(value[:end]), &previous) != nil || previous == "" {
			return false
		}
		expected, err := archive(previous)
		if err == nil && bytes.Equal(data, expected) {
			return true
		}
		legacy, err := archiveWithExtension(previous, legacyExtension)
		return err == nil && bytes.Equal(data, legacy)
	}
	return false
}

// Setup builds or removes the installer, without altering editor extensions.
func Setup(home string, remove bool) error {
	p := Path(home)
	data, err := archive(hookcmd.Path())
	if err != nil {
		return err
	}
	info, err := os.Lstat(p)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular Zoo extension archive at %s", p)
		}
		old, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading Zoo extension archive: %w", err)
		}
		if !bytes.Equal(old, data) && !managedArchive(old) {
			return fmt.Errorf("refusing to replace modified Zoo extension archive at %s", p)
		}
		if remove {
			return os.Remove(p)
		}
		if bytes.Equal(old, data) {
			return nil
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			return fmt.Errorf("updating Zoo extension archive: %w", err)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking Zoo extension archive: %w", err)
	}
	if remove {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Zoo extension directory: %w", err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		return fmt.Errorf("writing Zoo extension archive: %w", err)
	}
	return nil
}
