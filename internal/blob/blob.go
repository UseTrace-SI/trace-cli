package blob

import (
	"bytes"
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

// OpenReadOnly never creates source state. Missing referenced objects fail on
// lookup; an empty history need not have an objects directory yet.
func OpenReadOnly(dir string) (*Store, error) {
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("objects path is not a real directory")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

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
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("reading chunk manifest: %w", err)
	}
	var m chunkManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing chunk manifest: %w", err)
	}
	if len(b) > 8*1024*1024 {
		return nil, errors.New("blob metadata exceeds 8 MiB")
	}
	if len(m.Chunks) == 0 || len(m.Chunks) > 100000 {
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
	prefix, err := io.ReadAll(io.LimitReader(r, chunkThreshold))
	if err != nil {
		return "", fmt.Errorf("reading blob: %w", err)
	}
	if len(prefix) < chunkThreshold {
		return s.PutBytes(prefix)
	}
	return s.storeStream(io.MultiReader(bytes.NewReader(prefix), r))
}

// storeStream keeps at most one 64 KiB chunk and a read buffer in memory.
// The manifest is published only after EOF, with the whole-file content hash.
func (s *Store) storeStream(r io.Reader) (Hash, error) {
	sum := sha256.New()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return "", err
	}
	defer encoder.Close()
	manifest := chunkManifest{}
	data := make([]byte, 0, 64*1024)
	buffer := make([]byte, 64*1024)
	var rolling uint64
	var window [64]byte
	position, count := 0, 0
	flush := func() error {
		if len(manifest.Chunks) >= 100000 {
			return errors.New("blob exceeds 100000 chunks")
		}
		digest := sha256.Sum256(data)
		ch := Hash(hex.EncodeToString(digest[:]))
		chunkPath, err := s.chunkPath(ch)
		if err != nil {
			return err
		}
		if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
			if err := writeObject(chunkPath, encoder.EncodeAll(data, nil)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		manifest.Chunks = append(manifest.Chunks, string(ch))
		data = data[:0]
		rolling = 0
		position = 0
		count = 0
		return nil
	}
	emptyReads := 0
	for {
		n, err := r.Read(buffer)
		if n > 0 {
			emptyReads = 0
			_, _ = sum.Write(buffer[:n])
		} else if err == nil {
			emptyReads++
			if emptyReads >= 100 {
				return "", io.ErrNoProgress
			}
		}
		for _, b := range buffer[:n] {
			data = append(data, b)
			rolling = rolling*uint64(0x9e3779b97f4a7c15) + uint64(b) + 1
			if count >= len(window) {
				rolling -= uint64(window[position]) + 1
			} else {
				count++
			}
			window[position] = b
			position = (position + 1) % len(window)
			if len(data) >= 8*1024 && (len(data) >= 64*1024 || rolling&uint64(32*1024-1) == 0) {
				if e := flush(); e != nil {
					return "", e
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading blob: %w", err)
		}
	}
	if len(data) > 0 {
		if err := flush(); err != nil {
			return "", err
		}
	}
	h := Hash(hex.EncodeToString(sum.Sum(nil)))
	p, err := s.objectPath(h)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := writeObject(p, encoded); err != nil {
		return "", err
	}
	return h, nil
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
		if err := writeObject(p, compressed); err != nil {
			return "", err
		}
		return h, nil
	}
	return s.storeStream(bytes.NewReader(data))
}

func (s *Store) Get(h Hash) (io.ReadCloser, error) {
	p, err := s.objectPath(h)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if len(data) > 8*1024*1024 {
		return nil, errors.New("stored object exceeds 8 MiB")
	}
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
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("creating decompressor: %w", err)
	}
	return &verifyReader{decoder: dec, file: f, expected: string(h), hash: sha256.New(), limit: 8 << 20}, nil
}

// getChunked opens at most one chunk at a time. Closing a partial read releases
// it synchronously; there is no producer goroutine or GC-dependent cleanup.
func (s *Store) getChunked(h Hash, m chunkManifest) (io.ReadCloser, error) {
	if len(m.Chunks) > 100000 {
		return nil, errors.New("blob exceeds 100000 chunks")
	}
	for _, ch := range m.Chunks {
		if _, err := s.chunkPath(Hash(ch)); err != nil {
			return nil, err
		}
	}
	return &chunkedReader{store: s, chunks: m.Chunks, expected: string(h), hash: sha256.New()}, nil
}

type chunkedReader struct {
	store    *Store
	chunks   []string
	next     int
	current  io.ReadCloser
	expected string
	hash     hash.Hash
	closed   bool
	terminal error
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.terminal != nil {
		return 0, r.terminal
	}
	for {
		if r.current == nil {
			if r.next == len(r.chunks) {
				r.terminal = io.EOF
				if hex.EncodeToString(r.hash.Sum(nil)) != r.expected {
					r.terminal = errors.New("blob hash mismatch")
				}
				return 0, r.terminal
			}
			reader, err := r.store.GetChunk(Hash(r.chunks[r.next]))
			if err != nil {
				r.terminal = err
				return 0, err
			}
			r.current = reader
			r.next++
		}
		n, err := r.current.Read(p)
		if n > 0 {
			_, _ = r.hash.Write(p[:n])
		}
		if err != nil {
			closeErr := r.current.Close()
			r.current = nil
			if err != io.EOF {
				r.terminal = err
				return n, err
			}
			if closeErr != nil {
				r.terminal = closeErr
				return n, closeErr
			}
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (r *chunkedReader) Close() error {
	r.closed = true
	if r.current != nil {
		err := r.current.Close()
		r.current = nil
		return err
	}
	return nil
}

// GetChunk exposes a verified, bounded chunk for the sync protocol.
func (s *Store) GetChunk(h Hash) (io.ReadCloser, error) {
	path, err := s.chunkPath(h)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	decoder, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		f.Close()
		return nil, err
	}
	return &verifyReader{decoder: decoder, file: f, hash: sha256.New(), expected: string(h), limit: 64 << 10}, nil
}

// PutChunk stores a network chunk only after validating its content hash.
func (s *Store) PutChunk(h Hash, data []byte) error {
	if len(data) > 64*1024 {
		return errors.New("chunk exceeds 64 KiB")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != string(h) {
		return errors.New("chunk hash mismatch")
	}
	path, err := s.chunkPath(h)
	if err != nil {
		return err
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return err
	}
	compressed := enc.EncodeAll(data, nil)
	enc.Close()
	return writeObject(path, compressed)
}

// PutChunked verifies every referenced chunk and the whole-file hash before
// making a recovered blob visible. Existing history is never overwritten.
func (s *Store) PutChunked(h Hash, chunks []Hash) error {
	if len(chunks) == 0 {
		return errors.New("empty chunk manifest")
	}
	m := chunkManifest{}
	for _, chunk := range chunks {
		m.Chunks = append(m.Chunks, string(chunk))
	}
	r, err := s.getChunked(h, m)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, r)
	closeErr := r.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	path, err := s.objectPath(h)
	if err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeObject(path, data)
}

func writeObject(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return nil
}

// verifyReader streams decompressed content and verifies the content hash once
// the consumer reaches EOF, so large blobs never have to sit in memory twice.
type verifyReader struct {
	decoder  *zstd.Decoder
	file     *os.File
	hash     hash.Hash
	expected string
	checked  bool
	limit    int64
	read     int64
}

func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.decoder.Read(p)
	v.read += int64(n)
	if v.read > v.limit {
		return 0, errors.New("decoded object exceeds its size limit")
	}
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
