package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

func (s *Store) Put(r io.Reader) (Hash, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("reading blob: %w", err)
	}
	return s.PutBytes(b)
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
	compressed, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading blob: %w", err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("creating decompressor: %w", err)
	}
	b, err := dec.DecodeAll(compressed, nil)
	dec.Close()
	if err != nil {
		return nil, fmt.Errorf("decompressing blob: %w", err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != string(h) {
		return nil, errors.New("blob hash mismatch")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *Store) Has(h Hash) bool {
	p, err := s.objectPath(h)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
