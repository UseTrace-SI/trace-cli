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
