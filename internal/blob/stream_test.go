package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type smallReads struct{ io.Reader }

func (r smallReads) Read(p []byte) (int, error) {
	if len(p) > 113 {
		p = p[:113]
	}
	return r.Reader.Read(p)
}

func TestDecompressedObjectsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		chunk bool
	}{{"legacy", 9 << 20, false}, {"chunk", 65 << 10, true}} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte("x"), tc.size)
			digest := sha256.Sum256(data)
			h := Hash(hex.EncodeToString(digest[:]))
			bs, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p, err := bs.objectPath(h)
			if tc.chunk {
				p, err = bs.chunkPath(h)
			}
			if err != nil {
				t.Fatal(err)
			}
			encoder, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}
			compressed := encoder.EncodeAll(data, nil)
			encoder.Close()
			if err := writeObject(p, compressed); err != nil {
				t.Fatal(err)
			}
			var reader io.ReadCloser
			if tc.chunk {
				reader, err = bs.GetChunk(h)
			} else {
				reader, err = bs.Get(h)
			}
			if err != nil {
				return
			} // Rejecting an excessive frame up front is valid.
			defer reader.Close()
			if _, err := io.Copy(io.Discard, reader); err == nil {
				t.Fatal("accepted an oversized decompressed object")
			}
			if _, err := os.Stat(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestStreamingPreservesChunkBoundaries(t *testing.T) {
	data := make([]byte, 2<<20)
	var x uint32 = 17
	for i := range data {
		x = x*1664525 + 1013904223
		data[i] = byte(x >> 24)
	}
	boundaries := chunkBoundaries(data)
	for _, reader := range []io.Reader{bytes.NewReader(data), smallReads{bytes.NewReader(data)}} {
		bs, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		h, err := bs.Put(reader)
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := bs.Chunks(h)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunks) != len(boundaries)-1 {
			t.Fatalf("boundary count changed: %d %d", len(chunks), len(boundaries)-1)
		}
		for i, ch := range chunks {
			digest := sha256.Sum256(data[boundaries[i]:boundaries[i+1]])
			if string(ch) != hex.EncodeToString(digest[:]) {
				t.Fatalf("chunk %d changed across reader boundaries", i)
			}
		}
	}
}
