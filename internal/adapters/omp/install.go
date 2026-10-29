package omp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/pi"
)

const relative = ".omp/extensions/trace/index.ts"

func Install(root string) error {
	path := filepath.Join(root, relative)
	if existing, err := os.ReadFile(path); err == nil && !pi.IsManaged(existing) {
		return fmt.Errorf("refusing to replace user Oh My Pi extension at %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Oh My Pi extension: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating Oh My Pi extension directory: %w", err)
	}
	if err := os.WriteFile(path, pi.Render(root, "omp"), 0600); err != nil {
		return fmt.Errorf("writing Oh My Pi extension: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	path := filepath.Join(root, relative)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Oh My Pi extension: %w", err)
	}
	if !pi.IsManaged(b) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing Oh My Pi extension: %w", err)
	}
	return nil
}
