package adapters

// registry lists every adapter. Global integrations appear first, in the
// order `trace agents` lists them and `trace setup --help` names them.
var registry = []Adapter{
	claudeCode{},
	codexAdapter{},
	cursorAdapter{},
	windsurfAdapter{},
	copilotAdapter{},
	geminiAdapter{},
	qwenAdapter{},
	factoryAdapter{},
	ampAdapter{},
	newPi(),
	newOMP(),
	kiloAdapter{},
	opencodeAdapter{},
	openclaudeAdapter{},
	kiroAdapter{},
	kimiAdapter{},
	auggieAdapter{},
	openhandsAdapter{},
	clineAdapter{},
	poolsideAdapter{},
	hermesAdapter{},
	zooAdapter{},
	agyAdapter{},
	grokAdapter{},
	gooseAdapter{},
	langgraphAdapter{},
	crewaiAdapter{},
	aiderAdapter{},
	codebuffAdapter{},
	gitAdapter{},
}

// projectRemovalOrder is the order `trace uninstall` removes project
// hooks; the first failure stops it. Project installers not listed here
// follow in registry order.
var projectRemovalOrder = []string{"claude-code", "cline", "openclaude", "openhands", "cursor", "windsurf", "agy", "gemini", "copilot", "qwen", "factory", "pi", "omp", "grok", "goose", "amp", "kilo", "kiro", "langgraph", "crewai", "aider", "codebuff"}

// All returns every registered adapter in registry order.
func All() []Adapter {
	return append([]Adapter(nil), registry...)
}

// Lookup returns the adapter with the canonical name.
func Lookup(name string) (Adapter, bool) {
	for _, a := range registry {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// ForHook resolves the agent name of `trace hook <name> <event>`. global
// reports a user-level alias. Unknown names decode the generic payload.
func ForHook(name string) (a Adapter, global bool) {
	for _, candidate := range registry {
		if candidate.Name() == name {
			return candidate, false
		}
		for _, alias := range candidate.HookAliases() {
			if alias == name {
				return candidate, true
			}
		}
	}
	return genericAdapter{name: name}, false
}

// ForProject returns an agent that can be enabled in one repository: a
// ProjectInstaller or a MarkerEnabled adapter.
func ForProject(name string) (Adapter, bool) {
	a, ok := Lookup(name)
	if !ok {
		return nil, false
	}
	switch a.(type) {
	case ProjectInstaller, MarkerEnabled:
		return a, true
	}
	return nil, false
}

// ForGlobal returns the agent's user-level installer.
func ForGlobal(name string) (GlobalInstaller, bool) {
	a, ok := Lookup(name)
	if !ok {
		return nil, false
	}
	g, ok := a.(GlobalInstaller)
	return g, ok
}

// Globals returns the user-level installers in registry order.
func Globals() []GlobalInstaller {
	var out []GlobalInstaller
	for _, a := range registry {
		if g, ok := a.(GlobalInstaller); ok {
			out = append(out, g)
		}
	}
	return out
}

// GlobalNames returns the names of the user-level installers.
func GlobalNames() []string {
	var names []string
	for _, g := range Globals() {
		names = append(names, g.Name())
	}
	return names
}

// ProjectRemovers returns the project hook installers in the order `trace
// uninstall` removes them.
func ProjectRemovers() []ProjectInstaller {
	var out []ProjectInstaller
	listed := map[string]bool{}
	for _, name := range projectRemovalOrder {
		listed[name] = true
		if a, ok := Lookup(name); ok {
			if p, ok := a.(ProjectInstaller); ok {
				out = append(out, p)
			}
		}
	}
	for _, a := range registry {
		if p, ok := a.(ProjectInstaller); ok && !listed[a.Name()] {
			out = append(out, p)
		}
	}
	return out
}

// Markers returns the marker-enabled agents in registry order.
func Markers() []MarkerEnabled {
	var out []MarkerEnabled
	for _, a := range registry {
		if m, ok := a.(MarkerEnabled); ok {
			out = append(out, m)
		}
	}
	return out
}

// SessionReaders returns the agents whose sessions the daemon reads, in
// registry order.
func SessionReaders() []SessionReader {
	var out []SessionReader
	for _, a := range registry {
		if r, ok := a.(SessionReader); ok {
			out = append(out, r)
		}
	}
	return out
}
