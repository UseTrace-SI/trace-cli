package syncclient

import (
	"bytes"
	"encoding/hex"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSavedProSyncKeyRestoresCipher(t *testing.T) {
	salt := bytes.Repeat([]byte{3}, 32)
	cipher, err := Derive("a sufficiently long passphrase", salt)
	if err != nil {
		t.Fatal(err)
	}
	encoded := cipher.KeyForCredentialStore()
	restored, err := FromKey(encoded, salt)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal([]byte("private history"), "turn:1")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := restored.Open(sealed, "turn:1")
	if err != nil || string(opened) != "private history" {
		t.Fatalf("restored key opened %q, err=%v", opened, err)
	}
}

func TestCredentialSaveCommandKeepsTokenOutOfArguments(t *testing.T) {
	const value = `{"access_token":"sample-sensitive-token"}`
	for _, tc := range []struct {
		goos      string
		wantArgs  []string
		wantInput string
	}{
		{"darwin", []string{"security", "-i"}, "add-generic-password -U -a \"owner/repo\" -s " + service + " -X " + hex.EncodeToString([]byte(value)) + "\n"},
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

func TestMacCredentialTransportRejectsCommandInjectionAndOversize(t *testing.T) {
	for _, account := range []string{"owner/repo\nhelp", "owner/repo\rhelp", "owner/repo\x00help"} {
		if _, err := credentialSaveCommand("darwin", account, "value"); err == nil {
			t.Fatal("accepted a command separator in the account")
		}
	}
	if _, err := credentialSaveCommand("darwin", "owner/repo", strings.Repeat("x", 4096)); err == nil {
		t.Fatal("accepted a truncated security command")
	}
	cmd, err := credentialSaveCommand("darwin", `personal|https://trace.example/a"b\c|owner/repo`, `{"access_token":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	input, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(input), `-a "personal|https://trace.example/a\"b\\c|owner/repo"`) {
		t.Fatal("account quoting does not preserve special characters")
	}
}
