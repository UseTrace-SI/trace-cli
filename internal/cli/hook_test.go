package cli

import "testing"

func TestParseHookArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		repo    string
		agent   string
		event   string
		ok      bool
		handled bool
	}{
		{name: "installed hook", args: []string{"hook", "agy", "PreInvocation"}, agent: "agy", event: "PreInvocation", ok: true, handled: true},
		{name: "leading repo", args: []string{"--repo", "/tmp/project", "hook", "cursor", "stop"}, repo: "/tmp/project", agent: "cursor", event: "stop", ok: true, handled: true},
		{name: "trailing repo", args: []string{"hook", "claude-code", "Stop", "--repo=/tmp/project", "--json"}, repo: "/tmp/project", agent: "claude-code", event: "Stop", ok: true, handled: true},
		{name: "incomplete hook", args: []string{"hook", "agy"}, handled: true},
		{name: "help", args: []string{"hook", "--help"}},
		{name: "other command", args: []string{"log", "hook"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, agent, event, ok, handled := parseHookArgs(tt.args)
			if repo != tt.repo || agent != tt.agent || event != tt.event || ok != tt.ok || handled != tt.handled {
				t.Fatalf("parseHookArgs(%q) = (%q, %q, %q, %t, %t)", tt.args, repo, agent, event, ok, handled)
			}
		})
	}
}
