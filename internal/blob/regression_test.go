package blob

import (
	"bytes"
	"io"
	"os"
	"runtime/debug"
	"testing"
)

func TestRegressionChunkCloseAndIntegrity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("a"), 256*1024)
	h, err := s.PutBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	count := func() int {
		t.Helper()
		// Read names without statting entries: /dev/fd can expose transient
		// descriptors that are already closed by the time ReadDir stats them
		// on macOS (EBADF). The directory's own descriptor is counted in both
		// samples and is closed before returning.
		dir, err := os.Open("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		defer dir.Close()
		entries, err := dir.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for i := 0; i < 5; i++ {
		r, err := s.Get(h)
		if err != nil {
			t.Fatal(err)
		}
		var first [1]byte
		if _, err := r.Read(first[:]); err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("descriptor leak without GC: before=%d after=%d", before, after)
	}
	chunks, err := s.Chunks(h)
	if err != nil {
		t.Fatal(err)
	}
	// Replacing a valid chunk with different valid compressed data must fail
	// its own hash check, not depend only on reading the whole parent to EOF.
	other, err := s.PutBytes([]byte("corrupt"))
	if err != nil {
		t.Fatal(err)
	}
	source, _ := s.objectPath(other)
	target, _ := s.chunkPath(chunks[0])
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, b, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
	r.Close()
	if after := count(); after != before {
		t.Fatalf("error path descriptor leak: %d -> %d", before, after)
	}
	if err := s.PutChunked(h, chunks); err == nil {
		t.Fatal("published corrupt manifest")
	}
}

func TestRegressionMissingChunkDoesNotLeak(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.PutBytes(bytes.Repeat([]byte("a"), 256*1024))
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := s.Chunks(h)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.chunkPath(chunks[0])
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("missing chunk accepted")
	}
}
