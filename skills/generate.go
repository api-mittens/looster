// Package skills ships the agent-facing Looster instructions with the binary.
package skills

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed looster/SKILL.md
var content []byte

// Generate writes the bundled skill into a skills root. Identical files are left
// alone; replacing different content requires force. No configuration or secrets
// are included in the skill.
func Generate(dir string, force bool) (string, error) {
	return generate(dir, force, os.UserHomeDir)
}

func generate(dir string, force bool, userHome func() (string, error)) (string, error) {
	if dir == "" || dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := userHome()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if dir == "" {
			dir = filepath.Join(home, ".agents", "skills")
		} else {
			dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
		}
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(root, "looster")
	if info, err := os.Lstat(folder); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("skill directory %s must be a directory, not a file or symlink", folder)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(folder, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(folder, "SKILL.md")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("skill file %s must be a regular file", path)
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if bytes.Equal(existing, content) {
			return path, nil
		}
		if !force {
			return "", fmt.Errorf("skill %s already exists with different content; use --force to replace it", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !force {
		// Exclusive creation also protects a file created since the check above.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return "", err
		}
		_, writeErr := f.Write(content)
		closeErr := f.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
	// Stage replacements in the same directory so readers see a complete file.
	f, err := os.CreateTemp(folder, ".SKILL-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(content)
	modeErr := f.Chmod(0644)
	closeErr := f.Close()
	if err := errors.Join(writeErr, modeErr, closeErr); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
