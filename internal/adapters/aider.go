package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/aider"

// aiderAdapter: the Trace Aider wrapper in .trace/integrations sends generic payloads.
type aiderAdapter struct{ GenericHooks }

func (aiderAdapter) Name() string { return "aider" }

func (aiderAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Aider", Verification: Preview}
}

func (aiderAdapter) InstallProject(root string) error { return aider.Install(root) }
func (aiderAdapter) RemoveProject(root string) error  { return aider.Uninstall(root) }
