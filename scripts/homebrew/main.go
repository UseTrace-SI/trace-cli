package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func formula(version string, checksums []byte) ([]byte, error) {
	if !versionPattern.MatchString(version) {
		return nil, errors.New("invalid release version")
	}
	known := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		if len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		known[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading checksums: %w", err)
	}
	var b strings.Builder
	b.WriteString("class Trace < Formula\n")
	b.WriteString("  desc \"Local coding-agent history and rewind\"\n")
	b.WriteString("  homepage \"https://usetrace.si\"\n")
	fmt.Fprintf(&b, "  version %q\n", version)
	b.WriteString("  license \"Apache-2.0\"\n\n")
	for _, osName := range []string{"darwin", "linux"} {
		platform := "macos"
		if osName == "linux" {
			platform = "linux"
		}
		fmt.Fprintf(&b, "  on_%s do\n", platform)
		for _, arch := range []struct{ goArch, homebrew string }{{"arm64", "arm"}, {"amd64", "intel"}} {
			archive := fmt.Sprintf("trace_%s_%s_%s.tar.gz", version, osName, arch.goArch)
			sha, ok := known[archive]
			if !ok {
				return nil, fmt.Errorf("missing checksum for %s", archive)
			}
			fmt.Fprintf(&b, "    on_%s do\n", arch.homebrew)
			fmt.Fprintf(&b, "      url %q\n", "https://github.com/UseTrace-SI/trace-cli/releases/download/v"+version+"/"+archive)
			fmt.Fprintf(&b, "      sha256 %q\n", sha)
			b.WriteString("    end\n")
		}
		b.WriteString("  end\n\n")
	}
	b.WriteString("  def install\n    bin.install \"trace\"\n    bin.install_symlink bin/\"trace\" => \"trc\"\n  end\n")
	b.WriteString("end\n")
	return []byte(b.String()), nil
}

func run(version, checksumsPath, outPath string) error {
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}
	data, err := formula(version, checksums)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(outPath), ".trace-formula-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), outPath)
}

func main() {
	version := flag.String("version", "", "release version without v")
	checksums := flag.String("checksums", "", "GoReleaser checksum file")
	out := flag.String("out", "", "formula output path")
	flag.Parse()
	if *version == "" || *checksums == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "version, checksums, and out are required")
		os.Exit(2)
	}
	if err := run(*version, *checksums, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
