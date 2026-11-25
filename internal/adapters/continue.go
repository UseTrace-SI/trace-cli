package adapters

// continueAdapter: Continue's CLI (cn) has single-prompt capture through
// `trace run continue`.
type continueAdapter struct{ GenericHooks }

func (continueAdapter) Name() string { return "continue" }

func (continueAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Continue", Verification: Preview}
}

func (continueAdapter) RunCommand(prompt string) (string, []string) {
	return "cn", []string{"-p", prompt}
}

func (continueAdapter) RunCapturesOutput() bool { return false }
