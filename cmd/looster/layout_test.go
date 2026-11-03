package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Discover the module root, not the Git directory: isolated checkouts have a
// .git file, and source archives may have no Git metadata at all.
func TestPrivatePackageLayout(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("cannot find module root")
		}
		root = parent
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "internal/") && filepath.ToSlash(filepath.Dir(rel)) != "cmd/looster" {
			t.Errorf("private Go source must live under internal/: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
