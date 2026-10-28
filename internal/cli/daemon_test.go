package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLogRotatesWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	log, err := openRotatingLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"123456", "7890", "x"} {
		if _, err := log.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "x" || string(previous) != "1234567890" {
		t.Fatalf("current=%q previous=%q", current, previous)
	}
}
