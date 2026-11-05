package manifest

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type capturePolicy struct {
	fileBytes, totalBytes, minFree int64
	excludes                       []string
}

func positiveLimit(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive byte count", name)
	}
	return n, nil
}

func loadPolicy(root string) (capturePolicy, error) {
	p := capturePolicy{}
	var err error
	if p.fileBytes, err = positiveLimit("TRACE_MAX_FILE_BYTES", 64<<20); err != nil {
		return p, err
	}
	if p.totalBytes, err = positiveLimit("TRACE_MAX_CAPTURE_BYTES", 512<<20); err != nil {
		return p, err
	}
	if p.minFree, err = positiveLimit("TRACE_MIN_FREE_BYTES", 64<<20); err != nil {
		return p, err
	}
	ignorePath := filepath.Join(root, ".traceignore")
	if info, statErr := os.Lstat(ignorePath); statErr == nil && (!info.Mode().IsRegular() || info.Size() > 65536) {
		return p, fmt.Errorf(".traceignore must be a regular file at most 64 KiB")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return p, statErr
	}
	f, err := os.Open(ignorePath)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return p, err
	}
	if len(b) > 65536 {
		return p, fmt.Errorf(".traceignore exceeds 64 KiB")
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") || strings.Contains(line, "**") {
			return p, fmt.Errorf(".traceignore supports relative paths, directory prefixes and single-level globs; ! and ** are unsupported")
		}
		if _, err := path.Match(line, ""); err != nil {
			return p, fmt.Errorf("invalid .traceignore pattern: %w", err)
		}
		p.excludes = append(p.excludes, strings.TrimPrefix(line, "/"))
	}
	return p, nil
}

func (p capturePolicy) excluded(rel string) bool {
	for _, pattern := range p.excludes {
		if strings.HasSuffix(pattern, "/") {
			base := strings.TrimSuffix(pattern, "/")
			if rel == base || strings.HasPrefix(rel, pattern) {
				return true
			}
		}
		if match, _ := path.Match(pattern, rel); match {
			return true
		}
		if !strings.Contains(pattern, "/") {
			for _, part := range strings.Split(rel, "/") {
				if match, _ := path.Match(pattern, part); match {
					return true
				}
			}
		}
	}
	return false
}

func (p capturePolicy) check(root, rel string, size, total int64) error {
	if size > p.fileBytes {
		return fmt.Errorf("capture file %s exceeds TRACE_MAX_FILE_BYTES (%d); exclude it in .traceignore or raise the limit", rel, p.fileBytes)
	}
	if size > p.totalBytes-total {
		return fmt.Errorf("capture exceeds TRACE_MAX_CAPTURE_BYTES (%d)", p.totalBytes)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return fmt.Errorf("checking capture disk space: %w", err)
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	if uint64(p.minFree) > free || uint64(size) > free-uint64(p.minFree) {
		return fmt.Errorf("capture paused: insufficient disk space above TRACE_MIN_FREE_BYTES (%d)", p.minFree)
	}
	return nil
}

// CaptureExcluded applies project capture policy. Built-in secret exclusions
// remain unconditional, even when a user changes .traceignore.
func CaptureExcluded(root, rel string) (bool, error) {
	p, err := loadPolicy(root)
	return Excluded(rel) || p.excluded(rel), err
}

type boundedFile struct {
	io.Reader
	remaining int64
	read      int64
}

func (r *boundedFile) Read(p []byte) (int, error) {
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.Reader.Read(p)
	r.remaining -= int64(n)
	r.read += int64(n)
	if r.remaining < 0 {
		return 0, fmt.Errorf("file grew beyond its capture limit")
	}
	return n, err
}
