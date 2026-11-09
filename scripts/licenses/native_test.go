package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedNativeSnapshot(t *testing.T) {
	root, err := rootDir()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := appendNative(&out, root); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"# THE TPC SOFTWARE IS AVAILABLE WITHOUT CHARGE FROM TPC.",
		"DuckDB v1.5.2 native static engine", duckdbCommit,
		"extension/icu/third_party/icu/LICENSE", "IPADIC/ICOT",
		"extension/tpch/dbgen/LICENSE", "TPC EULA v2.2 (restricted, NOT MIT",
		"third_party/utf8proc/LICENSE", "third_party/mbedtls/LICENSE",
		"third_party/zstd/LICENSE", "third_party/parquet/LICENSE",
	} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("missing native notice %q", expected)
		}
	}
}

func TestNativeSnapshotTamperingFails(t *testing.T) {
	root, err := rootDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "scripts/licenses/duckdb-notices.md"))
	if err != nil {
		t.Fatal(err)
	}
	// These collections must never be relabeled by a partial content heuristic.
	for _, name := range []string{"extension/icu/third_party/icu/LICENSE", "extension/tpch/dbgen/LICENSE"} {
		if _, err := noticeClassification(notice{name, "MIT License Permission is hereby granted, free of charge. THE SOFTWARE IS PROVIDED \"AS IS\"."}); err == nil {
			t.Errorf("changed collection %s accepted", name)
		}
	}
	if len(data) < 100000 {
		t.Fatal("native source inventory unexpectedly truncated")
	}
}

func TestNativeSourceRequiresPinnedCheckout(t *testing.T) {
	if _, err := nativeNotices("", t.TempDir()); err == nil {
		t.Fatal("unversioned native source accepted")
	}
}

func TestPlatformTestInventoryIsHostIndependent(t *testing.T) {
	root, err := rootDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, module := range []string{
		"github.com/duckdb/duckdb-go/v2@v2.10502.0",
		"github.com/duckdb/duckdb-go-bindings@v0.10502.0",
	} {
		if !strings.Contains(text, "## "+module+"\n") {
			t.Errorf("missing shared driver module license %q", module)
		}
	}
	if strings.Contains(text, "Inventories: native tests") || strings.Contains(text, ", native tests") {
		t.Fatal("host-dependent native test membership reintroduced")
	}
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		expected := "Inventories: " + target + ", " + target + " tests."
		if !strings.Contains(text, expected) {
			t.Errorf("missing deterministic platform binding membership %q", expected)
		}
	}
	if strings.Contains(text, "windows/") || strings.Contains(text, "github.com/duckdb/duckdb-go-bindings/windows-") {
		t.Fatal("unsupported Windows release graph inventoried")
	}
}
