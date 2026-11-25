package adapters

// crushAdapter: Crush has single-prompt capture through `trace run crush`.
type crushAdapter struct{ GenericHooks }

func (crushAdapter) Name() string { return "crush" }

func (crushAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Crush", Verification: Preview}
}

func (crushAdapter) RunCommand(prompt string) (string, []string) {
	return "crush", []string{"run", "--", prompt}
}

func (crushAdapter) RunCapturesOutput() bool { return false }
