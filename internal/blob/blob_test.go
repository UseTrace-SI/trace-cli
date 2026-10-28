package blob

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

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
