package main

import (
	"os"

	"github.com/UseTrace-SI/trace-cli/internal/cli"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if cli.TryHook(os.Args[1:]) {
		return
	}
	os.Exit(cli.Execute(version, commit, date))
}
