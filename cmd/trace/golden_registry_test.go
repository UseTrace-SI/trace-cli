//go:build !windows

package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
)

// The golden tests name the agents they cover. A registered agent missing
// from these lists would silently go without golden coverage.
func TestGoldenAgentListsMatchRegistry(t *testing.T) {
	var project, global, launch, review, run []string
	aliases := map[string][]string{}
	for _, a := range adapters.All() {
		name := a.Name()
		if _, ok := adapters.ForProject(name); ok {
			project = append(project, name)
		}
		if _, ok := adapters.ForGlobal(name); ok {
			global = append(global, name)
		}
		if _, ok := a.(adapters.Launcher); ok {
			launch = append(launch, name)
		}
		if _, ok := a.(adapters.Reviewer); ok {
			review = append(review, name)
		}
		if _, ok := a.(adapters.Runner); ok {
			run = append(run, name)
		}
		if names := a.HookAliases(); len(names) > 0 {
			aliases[name] = names
		}
	}
	for _, list := range []struct {
		name     string
		golden   []string
		registry []string
	}{
		{"projectAgents", projectAgents, project},
		{"globalAgents", globalAgents, global},
		{"launchAgents", launchAgents, launch},
		{"reviewAgents", reviewAgents, review},
		{"runAgents", runAgents, run},
	} {
		golden := slices.Sorted(slices.Values(list.golden))
		registry := slices.Sorted(slices.Values(list.registry))
		if !slices.Equal(golden, registry) {
			t.Errorf("%s = %v, registry has %v", list.name, golden, registry)
		}
	}
	if !reflect.DeepEqual(aliases, hookAliases) {
		t.Errorf("hookAliases = %v, registry has %v", hookAliases, aliases)
	}
	// Every registered agent has recorded hook payloads.
	covered := map[string]bool{}
	for _, p := range loadHookPayloads(t) {
		covered[p.Agent] = true
	}
	for _, a := range adapters.All() {
		if !covered[a.Name()] {
			t.Errorf("%s has no payload in testdata/golden/hook_payloads.json", a.Name())
		}
	}
}
