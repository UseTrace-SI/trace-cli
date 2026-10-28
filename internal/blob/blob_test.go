package blob

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPutStreamsLargeContent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, maxMemoryPutBytes+1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	h, err := s.Put(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("large blob content changed")
	}
	if again, err := s.Put(bytes.NewReader(data)); err != nil || again != h {
		t.Fatalf("large blob not deduplicated: %q %v", again, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".*-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestPutGet(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"text", []byte("hello\nworld")}, {"binary", []byte{0, 1, 2, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h, err := s.PutBytes(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			h2, err := s.PutBytes(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if h != h2 || !s.Has(h) {
				t.Fatal("blob not deduplicated")
			}
			r, err := s.Get(h)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			if string(got) != string(tc.data) {
				t.Fatalf("got %q", got)
			}
			p, _ := s.objectPath(h)
			if _, err := os.Stat(filepath.Dir(p)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
