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

func main() { os.Exit(cli.Execute(version, commit, date)) }
