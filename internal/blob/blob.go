package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

type Hash string
type Store struct{ dir string }

const chunkSize = 32 * 1024
const chunkThreshold = 64 * 1024
const maxMemoryPutBytes = 4 << 20

type chunkManifest struct {
	Chunks []string `json:"chunks"`
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating objects directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0700); err != nil {
		return nil, fmt.Errorf("creating chunks directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) objectPath(h Hash) (string, error) {
	if len(h) != 64 {
		return "", errors.New("invalid blob hash")
	}
	if _, err := hex.DecodeString(string(h)); err != nil {
		return "", fmt.Errorf("invalid blob hash: %w", err)
	}
	return filepath.Join(s.dir, string(h[:2]), string(h[2:])), nil
}

func (s *Store) chunkPath(h Hash) (string, error) {
	if len(h) != 64 {
		return "", errors.New("invalid chunk hash")
	}
	if _, err := hex.DecodeString(string(h)); err != nil {
		return "", fmt.Errorf("invalid chunk hash: %w", err)
	}
	return filepath.Join(s.dir, "chunks", string(h[:2]), string(h[2:])), nil
}

// Chunks returns the chunk hashes referenced by a stored blob. Legacy blobs
// contain zstd data and have no child objects.
func (s *Store) Chunks(h Hash) ([]Hash, error) {
	p, err := s.objectPath(h)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("reading blob metadata: %w", err)
	}
	defer f.Close()
	var first [1]byte
	n, err := f.Read(first[:])
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("reading blob metadata: %w", err)
	}
	if n == 0 || first[0] != '{' {
		return nil, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewinding blob metadata: %w", err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading chunk manifest: %w", err)
	}
	var m chunkManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing chunk manifest: %w", err)
	}
	if len(m.Chunks) == 0 {
		return nil, fmt.Errorf("empty chunk manifest for %s", h)
	}
	children := make([]Hash, 0, len(m.Chunks))
	for _, value := range m.Chunks {
		child := Hash(value)
		if _, err := s.chunkPath(child); err != nil {
			return nil, fmt.Errorf("invalid chunk in %s: %w", h, err)
		}
		children = append(children, child)
	}
	return children, nil
}

func (s *Store) Put(r io.Reader) (Hash, error) {
	// Read up to threshold+1 to decide small vs large without loading huge files.
	prefix, err := io.ReadAll(io.LimitReader(r, maxMemoryPutBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading blob: %w", err)
	}
	if len(prefix) <= maxMemoryPutBytes {
		return s.PutBytes(prefix)
	}
	// Large input: spill remainder to a temp file.
	raw, err := os.CreateTemp(s.dir, ".raw-*")
	if err != nil {
		return "", fmt.Errorf("creating temporary blob: %w", err)
	}
	defer os.Remove(raw.Name())
	if err := raw.Chmod(0600); err != nil {
		raw.Close()
		return "", fmt.Errorf("setting blob mode: %w", err)
	}
	sum := sha256.New()
	_, _ = sum.Write(prefix)
	if _, err := raw.Write(prefix); err != nil {
		raw.Close()
		return "", fmt.Errorf("writing blob: %w", err)
	}
	if _, err := io.Copy(io.MultiWriter(raw, sum), r); err != nil {
		raw.Close()
		return "", fmt.Errorf("reading blob: %w", err)
	}
	if err := raw.Close(); err != nil {
		return "", fmt.Errorf("closing blob: %w", err)
	}
	h := Hash(hex.EncodeToString(sum.Sum(nil)))
	// read all bytes back for chunking
	data, err := os.ReadFile(raw.Name())
	if err != nil {
		return "", fmt.Errorf("reading blob: %w", err)
	}
	return s.storeBytes(h, data)
}

func (s *Store) PutBytes(b []byte) (Hash, error) {
	sum := sha256.Sum256(b)
	h := Hash(hex.EncodeToString(sum[:]))
	return s.storeBytes(h, b)
}

func (s *Store) storeBytes(h Hash, data []byte) (Hash, error) {
	p, err := s.objectPath(h)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return h, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking blob: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", fmt.Errorf("creating blob directory: %w", err)
	}
	if len(data) < chunkThreshold {
		// small: legacy whole-file compressed storage
		enc, err := zstd.NewWriter(nil)
		if err != nil {
			return "", fmt.Errorf("creating compressor: %w", err)
		}
		compressed := enc.EncodeAll(data, nil)
		enc.Close()
		tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
		if err != nil {
			return "", fmt.Errorf("creating temporary blob: %w", err)
		}
		defer os.Remove(tmp.Name())
		if err := tmp.Chmod(0600); err != nil {
			tmp.Close()
			return "", fmt.Errorf("setting blob mode: %w", err)
		}
		if _, err := tmp.Write(compressed); err != nil {
			tmp.Close()
			return "", fmt.Errorf("writing blob: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return "", fmt.Errorf("closing blob: %w", err)
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			return "", fmt.Errorf("publishing blob: %w", err)
		}
		return h, nil
	}
	// large: chunked storage
	manifest := chunkManifest{}
	bounds := chunkBoundaries(data)
	for i := 0; i+1 < len(bounds); i++ {
		chunk := data[bounds[i]:bounds[i+1]]
		csum := sha256.Sum256(chunk)
		ch := Hash(hex.EncodeToString(csum[:]))
		cp, err := s.chunkPath(ch)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(cp); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(cp), 0700); err != nil {
				return "", fmt.Errorf("creating chunk directory: %w", err)
			}
			enc, err := zstd.NewWriter(nil)
			if err != nil {
				return "", fmt.Errorf("creating compressor: %w", err)
			}
			compressed := enc.EncodeAll(chunk, nil)
			enc.Close()
			tmp, err := os.CreateTemp(filepath.Dir(cp), ".tmp-*")
			if err != nil {
				return "", fmt.Errorf("creating temporary chunk: %w", err)
			}
			defer os.Remove(tmp.Name())
			if err := tmp.Chmod(0600); err != nil {
				tmp.Close()
				return "", fmt.Errorf("setting chunk mode: %w", err)
			}
			if _, err := tmp.Write(compressed); err != nil {
				tmp.Close()
				return "", fmt.Errorf("writing chunk: %w", err)
			}
			if err := tmp.Close(); err != nil {
				return "", fmt.Errorf("closing chunk: %w", err)
			}
			if err := os.Rename(tmp.Name(), cp); err != nil {
				return "", fmt.Errorf("publishing chunk: %w", err)
			}
		} else if err != nil {
			return "", fmt.Errorf("checking chunk: %w", err)
		}
		manifest.Chunks = append(manifest.Chunks, string(ch))
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encoding manifest: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating temporary manifest: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("setting manifest mode: %w", err)
	}
	if _, err := tmp.Write(mb); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", fmt.Errorf("publishing manifest: %w", err)
	}
	return h, nil
}

func (s *Store) Get(h Hash) (io.ReadCloser, error) {
	p, err := s.objectPath(h)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading blob: %w", err)
	}
	// Try chunk manifest.
	var m chunkManifest
	if err := json.Unmarshal(data, &m); err == nil && len(m.Chunks) > 0 {
		return s.getChunked(h, m)
	}
	// Legacy whole-file compressed.
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("reading blob: %w", err)
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("creating decompressor: %w", err)
	}
	return &verifyReader{decoder: dec, file: f, expected: string(h), hash: sha256.New()}, nil
}

func (s *Store) getChunked(h Hash, m chunkManifest) (io.ReadCloser, error) {
	// Reassemble chunks into a temp file? Instead stream via io.MultiReader of chunk readers.
	var readers []io.Reader
	for _, ch := range m.Chunks {
		cp, err := s.chunkPath(Hash(ch))
		if err != nil {
			return nil, err
		}
		f, err := os.Open(cp)
		if err != nil {
			return nil, fmt.Errorf("reading chunk: %w", err)
		}
		dec, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("creating chunk decompressor: %w", err)
		}
		// wrap to close file when exhausted
		readers = append(readers, &chunkReader{dec: dec, file: f, hash: sha256.New(), expected: ch})
	}
	// Concatenate and verify whole hash.
	pr, pw := io.Pipe()
	go func() {
		wh := sha256.New()
		for _, r := range readers {
			if _, err := io.Copy(io.MultiWriter(pw, wh), r); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		// close chunk files
		for _, r := range readers {
			if cr, ok := r.(*chunkReader); ok {
				cr.Close()
			}
		}
		if hex.EncodeToString(wh.Sum(nil)) != string(h) {
			pw.CloseWithError(errors.New("blob hash mismatch"))
			return
		}
		pw.Close()
	}()
	return &pipeReadCloser{pr: pr}, nil
}

type chunkReader struct {
	dec      *zstd.Decoder
	file     *os.File
	hash     hash.Hash
	expected string
}

func (c *chunkReader) Read(p []byte) (int, error) { return c.dec.Read(p) }
func (c *chunkReader) Close() error {
	c.dec.Close()
	return c.file.Close()
}

type pipeReadCloser struct{ pr *io.PipeReader }

func (p *pipeReadCloser) Read(b []byte) (int, error) { return p.pr.Read(b) }
func (p *pipeReadCloser) Close() error               { return p.pr.Close() }

// verifyReader streams decompressed content and verifies the content hash once
// the consumer reaches EOF, so large blobs never have to sit in memory twice.
type verifyReader struct {
	decoder  *zstd.Decoder
	file     *os.File
	hash     hash.Hash
	expected string
	checked  bool
}

func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.decoder.Read(p)
	if n > 0 {
		_, _ = v.hash.Write(p[:n])
	}
	if err == io.EOF && !v.checked {
		v.checked = true
		if hex.EncodeToString(v.hash.Sum(nil)) != v.expected {
			return n, errors.New("blob hash mismatch")
		}
	}
	return n, err
}

func (v *verifyReader) Close() error {
	v.decoder.Close()
	return v.file.Close()
}

// chunkBoundaries returns content-defined cut points (start offsets) for data,
// with 0 first and len(data) last. Uses a Rabin rolling hash with a 64-byte
// window, min 8KB, max 64KB, target ~32KB.
func chunkBoundaries(data []byte) []int {
	const minSize = 8 * 1024
	const maxSize = 64 * 1024
	const target = 32 * 1024
	mask := uint64(target - 1)
	bounds := []int{0}
	if len(data) == 0 {
		return append(bounds, 0)
	}
	// rolling hash state
	var h uint64
	window := make([]byte, 0, 64)
	prime := uint64(0x9e3779b97f4a7c15)
	last := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		h = h*prime + uint64(b) + 1
		window = append(window, b)
		if len(window) > 64 {
			old := window[0]
			window = window[1:]
			h -= uint64(old) + 1
		}
		chunkLen := i + 1 - last
		if chunkLen >= minSize {
			if chunkLen >= maxSize || (h&mask == 0) {
				bounds = append(bounds, i+1)
				last = i + 1
				h = 0
				window = window[:0]
			}
		}
	}
	if bounds[len(bounds)-1] != len(data) {
		bounds = append(bounds, len(data))
	}
	return bounds
}

func (s *Store) Has(h Hash) bool {
	p, err := s.objectPath(h)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
