package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/omp"

// ompAdapter is Oh My Pi; see piFamily.
type ompAdapter struct{ piFamily }

func newOMP() ompAdapter { return ompAdapter{piFamily{name: "omp"}} }

func (ompAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Oh My Pi", Verification: Preview}
}

func (ompAdapter) InstallProject(root string) error { return omp.Install(root) }
func (ompAdapter) RemoveProject(root string) error  { return omp.Uninstall(root) }
