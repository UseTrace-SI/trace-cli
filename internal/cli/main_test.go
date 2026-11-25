package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
)

// Installed Git hooks run the current executable as `<bin> hook git <event>`.
// Under test that is this binary: build the hook's events exactly as
// `trace hook` does and process them in-process, as the daemon would.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		runTestHook(os.Args[2:])
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "trailer-hook" {
		source := ""
		if len(os.Args) > 3 {
			source = os.Args[3]
		}
		_ = addCommitTrailers("", os.Args[2], source)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runTestHook(args []string) {
	if len(args) != 2 || args[0] != "git" {
		return
	}
	root, err := rootFromFlag("")
	if err != nil {
		return
	}
	input, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	report := func(err error) {
		f, openErr := os.OpenFile(filepath.Join(root, ".trace", "test-hook-errors"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if openErr == nil {
			fmt.Fprintln(f, err)
			f.Close()
		}
	}
	e, err := engine.Open(root)
	if err != nil {
		report(err)
		return
	}
	defer e.Close()
	for _, ev := range adapters.GitHookEvents(args[1], input, root) {
		if err := e.Process(ev); err != nil {
			report(err)
		}
	}
}
