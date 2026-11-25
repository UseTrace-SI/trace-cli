package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/langgraph"

// langgraphAdapter: the Trace LangGraph callback in .trace/integrations sends generic payloads.
type langgraphAdapter struct{ GenericHooks }

func (langgraphAdapter) Name() string { return "langgraph" }

func (langgraphAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "LangGraph", Verification: Preview}
}

func (langgraphAdapter) InstallProject(root string) error { return langgraph.Install(root) }
func (langgraphAdapter) RemoveProject(root string) error  { return langgraph.Uninstall(root) }
