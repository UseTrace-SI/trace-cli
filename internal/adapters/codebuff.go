package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/codebuff"

// codebuffAdapter: the Trace Codebuff bridge in .trace/integrations sends generic payloads.
type codebuffAdapter struct{ GenericHooks }

func (codebuffAdapter) Name() string { return "codebuff" }

func (codebuffAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Codebuff", Verification: Preview}
}

func (codebuffAdapter) InstallProject(root string) error { return codebuff.Install(root) }
func (codebuffAdapter) RemoveProject(root string) error  { return codebuff.Uninstall(root) }
