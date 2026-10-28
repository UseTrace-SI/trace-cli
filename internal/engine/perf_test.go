package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func BenchmarkKeyframe100ChangedFilesIn10000FileRepo(b *testing.B) {
	root := b.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		b.Fatalf("git init: %s: %v", out, err)
	}
	for i := 0; i < 10000; i++ {
		p := filepath.Join(root, fmt.Sprintf("file-%05d.txt", i))
		if err := os.WriteFile(p, []byte("initial\n"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	e, err := Open(root)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for round := 0; round < b.N; round++ {
		b.StopTimer()
		ev := event.Event{Kind: event.TurnStart, Agent: "codex", AgentSessionID: "bench", Worktree: root, At: time.Now().UnixMilli()}
		if err := e.Process(ev); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			p := filepath.Join(root, fmt.Sprintf("file-%05d.txt", i))
			if err := os.WriteFile(p, []byte(fmt.Sprintf("round %d\n", round)), 0600); err != nil {
				b.Fatal(err)
			}
			ev.Kind = event.ToolEdit
			ev.Paths = []string{p}
			if err := e.Process(ev); err != nil {
				b.Fatal(err)
			}
		}
		ev.Kind = event.TurnEnd
		ev.Paths = nil
		b.StartTimer()
		if err := e.Process(ev); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRewind1000Files(b *testing.B) {
	root := b.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		b.Fatalf("git init: %s: %v", out, err)
	}
	paths := make([]string, 1000)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("file-%04d.txt", i))
		if err := os.WriteFile(paths[i], []byte("before\n"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	e, err := Open(root)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		b.Fatal(err)
	}
	for round := 0; round < b.N; round++ {
		b.StopTimer()
		for _, p := range paths {
			if err := os.WriteFile(p, []byte(fmt.Sprintf("after %d\n", round)), 0600); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if _, _, err := e.Rewind(0, "", false); err != nil {
			b.Fatal(err)
		}
	}
}
