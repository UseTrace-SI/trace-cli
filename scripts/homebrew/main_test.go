package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestFormula(t *testing.T) {
	var checksums strings.Builder
	for _, osName := range []string{"darwin", "linux"} {
		for _, arch := range []string{"arm64", "amd64"} {
			fmt.Fprintf(&checksums, "%064x  trace_1.0.0_%s_%s.tar.gz\n", len(checksums.String())+1, osName, arch)
		}
	}
	got, err := formula("1.0.0", []byte(checksums.String()))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"on_macos do", "on_linux do", "on_arm do", "on_intel do", "bin.install_symlink bin/\"trace\" => \"trc\"", "https://usetrace.si/downloads/v1.0.0/trace_1.0.0_linux_amd64.tar.gz", "bin.install \"trace\", \"trace-brain\", \"trace-graph\"", "#{bin}/trace-graph version"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(string(got), "github.com/UseTrace-SI/trace-cli/releases") {
		t.Fatal("formula points to legacy standalone archives")
	}
}

func TestFormulaRejectsDuplicateChecksums(t *testing.T) {
	line := strings.Repeat("a", 64) + "  trace_1.0.0_darwin_arm64.tar.gz\n"
	if _, err := formula("1.0.0", []byte(line+line)); err == nil {
		t.Fatal("duplicate checksum accepted")
	}
}

func TestFormulaRejectsMissingArchiveAndBadVersion(t *testing.T) {
	for _, tc := range []struct{ version, checksums string }{{"1.0.0", ""}, {"1.0.0\"; system(\"bad\")", ""}} {
		if _, err := formula(tc.version, []byte(tc.checksums)); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
