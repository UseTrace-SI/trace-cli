package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
)

func TestRunHeadlessRecordsAndPreservesExit(t *testing.T) {
	for _, tc := range []struct{ agent, binary, argsCheck string }{
		{"continue", "cn", `[ "$1" = '-p' ] && [ "$2" = 'Change main ghp_abcdefghijklmnopqrstuvwxyz' ]`},
		{"vibe", "vibe", `[ "$1" = '-p' ] && [ "$2" = 'Change main ghp_abcdefghijklmnopqrstuvwxyz' ]`},
		{"crush", "crush", `[ "$1" = 'run' ] && [ "$2" = '--' ] && [ "$3" = 'Change main ghp_abcdefghijklmnopqrstuvwxyz' ]`},
		{"aider", "aider", `[ "$1" = '--no-auto-commits' ] && [ "$2" = '--no-dirty-commits' ] && [ "$3" = '--message=Change main ghp_abcdefghijklmnopqrstuvwxyz' ]`},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			agentOutput := "agent output"
			if tc.agent == "vibe" {
				agentOutput += " ghp_abcdefghijklmnopqrstuvwxyz"
			}
			script := "#!/bin/sh\n" + tc.argsCheck + " || exit 9\nprintf '" + agentOutput + "'\nprintf 'after\\n' > main.txt\nexit 7\n"
			if err := os.WriteFile(filepath.Join(bin, tc.binary), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd := runCommand(&root)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{tc.agent, "--prompt", "Change main ghp_abcdefghijklmnopqrstuvwxyz"})
			err := cmd.Execute()
			var exit agentExit
			if !errors.As(err, &exit) || exit.code != 7 {
				t.Fatalf("exit = %v", err)
			}
			if output.String() != agentOutput {
				t.Fatalf("output changed: %q", output.String())
			}
			e, err := engine.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			var agent, prompt string
			if err := e.DB.QueryRow(`SELECT s.agent,t.prompt FROM turns t JOIN sessions s ON s.id=t.session_id`).Scan(&agent, &prompt); err != nil {
				t.Fatal(err)
			}
			if agent != tc.agent || !strings.HasPrefix(prompt, "Change main ") || strings.Contains(prompt, "ghp_abcdefghijklmnopqrstuvwxyz") {
				t.Fatalf("turn: %s %s", agent, prompt)
			}
			if tc.agent == "vibe" {
				var content string
				if err := e.DB.QueryRow(`SELECT content FROM messages WHERE role='assistant'`).Scan(&content); err != nil || !strings.Contains(content, "Agent output: agent output") || strings.Contains(content, "ghp_abcdefghijklmnopqrstuvwxyz") {
					t.Fatalf("recorded output = %q, %v", content, err)
				}
			}
			var count int
			if err := e.DB.QueryRow(`SELECT COUNT(*) FROM keyframes WHERE seq=1 AND tag='turn'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("keyframe: %d, %v", count, err)
			}
			var before, after string
			if err := e.DB.QueryRow(`SELECT (SELECT manifest_hash FROM keyframes WHERE seq=0), (SELECT manifest_hash FROM keyframes WHERE seq=1)`).Scan(&before, &after); err != nil || before == after {
				t.Fatalf("file snapshot did not change: %s %s, %v", before, after, err)
			}
			if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD").Run(); err == nil {
				t.Fatal("created git history")
			}
		})
	}
}

func TestBoundedOutputDropsPartialLastLine(t *testing.T) {
	var output boundedOutput
	data := "complete line\n" + strings.Repeat("x", 64<<10)
	if n, err := output.Write([]byte(data)); err != nil || n != len(data) {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := output.text(); got != "complete line\n" {
		t.Fatalf("captured %q", got)
	}
}

func TestCharacterDeviceDetection(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !characterDevice(f) {
		t.Fatal("character device was not detected")
	}
	if characterDevice(&bytes.Buffer{}) {
		t.Fatal("buffer was treated as a character device")
	}
	regular, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if characterDevice(regular) {
		t.Fatal("regular file was treated as a character device")
	}
}

func TestRunStillExecutesWhenRecordingUnavailable(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".trace"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "cn"), []byte("#!/bin/sh\nprintf 'ran'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := runCommand(&root)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"continue", "--prompt", "hello"})
	if err := cmd.Execute(); err != nil || output.String() != "ran" {
		t.Fatalf("agent did not run: %q, %v", output.String(), err)
	}
}
