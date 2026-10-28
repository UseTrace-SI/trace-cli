package syncclient

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestCredentialSaveCommandKeepsTokenOutOfArguments(t *testing.T) {
	const value = `{"access_token":"sample-sensitive-token"}`
	for _, tc := range []struct {
		goos      string
		wantArgs  []string
		wantInput string
	}{
		{"darwin", []string{"security", "add-generic-password", "-U", "-a", "owner/repo", "-s", service, "-w"}, value + "\n" + value + "\n"},
		{"linux", []string{"secret-tool", "store", "--label=Trace sync", service, "owner/repo"}, value},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			cmd, err := credentialSaveCommand(tc.goos, "owner/repo", value)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cmd.Args, tc.wantArgs) {
				t.Fatalf("credential command arguments differ: %v", cmd.Args)
			}
			if strings.Contains(strings.Join(cmd.Args, "\x00"), "sample-sensitive-token") {
				t.Fatal("credential token exposed in process arguments")
			}
			input, err := io.ReadAll(cmd.Stdin)
			if err != nil {
				t.Fatal(err)
			}
			if string(input) != tc.wantInput {
				t.Fatal("credential command received the wrong standard input")
			}
		})
	}
	if _, err := credentialSaveCommand("windows", "owner/repo", value); err == nil {
		t.Fatal("unsupported credential store accepted")
	}
}
