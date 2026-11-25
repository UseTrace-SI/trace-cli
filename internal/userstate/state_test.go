package userstate

import (
	"os"
	"testing"
)

func TestPauseResumePreservesOtherUserState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	if Disabled() {
		t.Fatal("existing installations start unpaused")
	}
	if err := SetDisabled(true); err != nil {
		t.Fatal(err)
	}
	path, _ := Path("device.json")
	if err := os.WriteFile(path, []byte("connection metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if !Disabled() {
		t.Fatal("pause marker ignored")
	}
	if err := SetDisabled(false); err != nil {
		t.Fatal(err)
	}
	if Disabled() {
		t.Fatal("resume did not clear marker")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "connection metadata" {
		t.Fatal("resume changed the saved login")
	}
}

func TestACPAgentsShareOnePauseMarker(t *testing.T) {
	t.Setenv("TRACE_HOME", t.TempDir())
	if AgentDisabled("acp:claude-agent-acp") {
		t.Fatal("ACP agent paused without a marker")
	}
	if !AgentDisabled("acp:Bad Name") || !AgentDisabled("acp:") {
		t.Fatal("invalid ACP agent names must stay paused")
	}
	if err := SetAgentDisabled("acp", true); err != nil {
		t.Fatal(err)
	}
	if !AgentDisabled("acp:claude-agent-acp") || !AgentDisabled("acp:codex-acp") {
		t.Fatal("acp marker does not pause ACP agents")
	}
}
