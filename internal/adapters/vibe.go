package adapters

// vibeAdapter: Mistral Vibe has single-prompt capture through `trace run
// vibe`.
type vibeAdapter struct{ GenericHooks }

func (vibeAdapter) Name() string { return "vibe" }

func (vibeAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Mistral Vibe", Verification: Preview}
}

func (vibeAdapter) RunCommand(prompt string) (string, []string) {
	return "vibe", []string{"-p", prompt}
}

// RunCapturesOutput: Vibe's output is the only record of its reasoning.
func (vibeAdapter) RunCapturesOutput() bool { return true }
