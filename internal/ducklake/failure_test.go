package ducklake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnavailableExtensionCache(t *testing.T) {
	for name := range libpqEnvironment {
		t.Setenv(name, "")
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory-canary")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the real platform cache resolver, before any network request.
	t.Setenv("HOME", blocked)
	t.Setenv("XDG_CACHE_HOME", blocked)
	t.Setenv("LocalAppData", blocked)
	lake, err := Open(context.Background(), config())
	if lake != nil {
		lake.Close()
		t.Fatal("unavailable cache returned an engine")
	}
	if err == nil || !strings.Contains(err.Error(), "extension cache creation") || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unavailable cache did not fail safely: %v", err)
	}
}

func TestEngineAndMissingExtensionCompatibilityGates(t *testing.T) {
	lake := engine(t)
	p, err := prepare(config())
	if err != nil {
		t.Fatal(err)
	}
	gate := strings.SplitN(p, ";", 2)[0]
	gate = strings.Replace(gate, EngineVersion, "incompatible-canary", 1)
	if err := lake.Exec(context.Background(), gate); err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("engine mismatch did not fail safely: %v", err)
	}
	for _, line := range strings.Split(p, "\n") {
		if strings.Contains(line, "SELECT count(*) FROM duckdb_extensions()") {
			if err := lake.Exec(context.Background(), line); err == nil {
				t.Fatal("missing required extensions passed compatibility gate")
			}
			return
		}
	}
	t.Fatal("production extension compatibility gate missing")
}
