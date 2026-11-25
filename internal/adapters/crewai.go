package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/crewai"

// crewaiAdapter: the Trace CrewAI listener in .trace/integrations sends generic payloads.
type crewaiAdapter struct{ GenericHooks }

func (crewaiAdapter) Name() string { return "crewai" }

func (crewaiAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "CrewAI", Verification: Preview}
}

func (crewaiAdapter) InstallProject(root string) error { return crewai.Install(root) }
func (crewaiAdapter) RemoveProject(root string) error  { return crewai.Uninstall(root) }
