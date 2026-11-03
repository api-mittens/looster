package app_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Domain packages may depend on one another, but never on application ports,
// delivery mechanisms or storage adapters. Keep this boundary explicit as the
// factory grows rather than relying only on package naming conventions.
func TestDomainDependencyBoundary(t *testing.T) {
	const module = "github.com/api-mittens/looster/internal/"
	domain := map[string]bool{"agent": true, "behaviour": true, "event": true, "memory": true, "namespace": true, "tag": true, "task": true}
	for pkg := range domain {
		entries, err := os.ReadDir(filepath.Join("..", pkg))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join("..", pkg, entry.Name())
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(name, module) {
					target := strings.Split(strings.TrimPrefix(name, module), "/")[0]
					if !domain[target] {
						t.Errorf("domain %s imports non-domain package %s", path, name)
					}
				} else if strings.Contains(strings.Split(name, "/")[0], ".") {
					t.Errorf("domain %s imports infrastructure dependency %s", path, name)
				}
			}
		}
	}
}

func TestApplicationDoesNotDependOnAdapters(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			for _, adapter := range []string{"postgres", "cli", "web", "jcode", "objectstore"} {
				if strings.HasPrefix(name, "github.com/api-mittens/looster/internal/"+adapter) {
					t.Errorf("application %s imports adapter %s", entry.Name(), name)
				}
			}
		}
	}
}
