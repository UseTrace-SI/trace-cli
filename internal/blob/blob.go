package blob

import (
	"crypto/sha256"
	"encoding/hex"
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

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating objects directory: %w", err)
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

// maxMemoryPutBytes bounds how much of a stream Put keeps in memory before it
// spills to disk. Most repository files fit, and their content is hashed
// without touching the filesystem.
const maxMemoryPutBytes = 4 << 20

// Put stores content addressed by its SHA-256 hash. Content is hashed before
// compression so writing a blob that already exists - the common case when a
// keyframe rescans an unchanged tree - costs only a read.
func (s *Store) Put(r io.Reader) (Hash, error) {
	prefix, err := io.ReadAll(io.LimitReader(r, maxMemoryPutBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading blob: %w", err)
	}
	if len(prefix) <= maxMemoryPutBytes {
		return s.PutBytes(prefix)
	}
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
	if err := compressFile(raw.Name(), p); err != nil {
		return "", err
	}
	return h, nil
}

// compressFile writes the compressed contents of src to dst without holding
// either the input or the output in memory.
func compressFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening blob: %w", err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary blob: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting blob mode: %w", err)
	}
	enc, err := zstd.NewWriter(tmp)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("creating compressor: %w", err)
	}
	if _, err := io.Copy(enc, in); err != nil {
		enc.Close()
		tmp.Close()
		return fmt.Errorf("compressing blob: %w", err)
	}
	if err := enc.Close(); err != nil {
		tmp.Close()
		return fmt.Errorf("compressing blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing blob: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("publishing blob: %w", err)
	}
	return nil
}

func (s *Store) PutBytes(b []byte) (Hash, error) {
	sum := sha256.Sum256(b)
	h := Hash(hex.EncodeToString(sum[:]))
	p, _ := s.objectPath(h)
	if _, err := os.Stat(p); err == nil {
		return h, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking blob: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", fmt.Errorf("creating blob directory: %w", err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return "", fmt.Errorf("creating compressor: %w", err)
	}
	compressed := enc.EncodeAll(b, nil)
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

func (s *Store) Get(h Hash) (io.ReadCloser, error) {
	p, err := s.objectPath(h)
	if err != nil {
		return nil, err
	}
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

func (s *Store) Has(h Hash) bool {
	p, err := s.objectPath(h)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
