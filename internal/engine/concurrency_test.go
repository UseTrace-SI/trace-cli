package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// GC replaces e.current; it must synchronize with Process, which reads and
// replaces the same manifest. Running both concurrently makes an unsynchronized
// access show up under the race detector.
func TestEngineGCAndProcessShareCurrentRaceFree(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}

	const iterations = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			base := event.Event{Agent: "codex", AgentSessionID: "race", Worktree: root, At: time.Now().UnixMilli()}
			base.Kind = event.TurnStart
			_ = e.Process(base)
			base.Kind = event.TurnEnd
			_ = e.Process(base)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_, _ = e.GC(30, false)
		}
	}()

	close(start)
	wg.Wait()
}

// Rewind also reaches e.current through recordManifest; it must be serialized
// with Process as well.
func TestEngineRewindAndProcessShareCurrentRaceFree(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	// Created after the baseline so rewinding to seq 0 has work to do and
	// reaches recordManifest instead of returning early on an empty plan.
	extra := filepath.Join(root, "extra.txt")

	const iterations = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			base := event.Event{Agent: "codex", AgentSessionID: "race", Worktree: root, At: time.Now().UnixMilli()}
			base.Kind = event.TurnStart
			_ = e.Process(base)
			base.Kind = event.TurnEnd
			_ = e.Process(base)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_ = os.WriteFile(extra, []byte("extra\n"), 0600)
			_, _, _ = e.Rewind(0, "", false)
		}
	}()

	close(start)
	wg.Wait()
}
