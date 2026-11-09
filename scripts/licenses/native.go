package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DuckDB's Go module licenses do not cover all native code in the static
// archives. Inventory the pinned engine and its vendored libraries separately.
const duckdbCommit = "8a5851971fae891f292c2714d86046ee018e9737"
const duckdbVersion = "v1.5.2"

func nativeNotices(root, source string) ([]byte, error) {
	cmd := exec.Command("git", "-C", source, "rev-parse", "HEAD")
	commit, err := cmd.Output()
	if err != nil || string(bytes.TrimSpace(commit)) != duckdbCommit {
		return nil, fmt.Errorf("expected DuckDB source commit %s, got %s: %v", duckdbCommit, commit, err)
	}
	if err := exec.Command("git", "-C", source, "diff", "--exit-code", "HEAD").Run(); err != nil {
		return nil, fmt.Errorf("native source checkout has tracked modifications: %w", err)
	}
	var out bytes.Buffer
	out.WriteString("# THE TPC SOFTWARE IS AVAILABLE WITHOUT CHARGE FROM TPC.\n\nThe upstream static archives include TPC benchmark extensions. Their restricted EULA is reproduced below, not represented as MIT. No separate fee is charged for this software.\n\n")
	fmt.Fprintf(&out, "## DuckDB %s native static engine\n\nUsage: **build**, all six release targets. Official duckdb-go/v2 v2.10502.0 uses bindings v0.10502.0. Five targets use upstream prebuilt archives. Windows arm64 builds the same source with ICU, JSON, Parquet, Autocomplete, TPC-H and TPC-DS statically linked. Source: https://github.com/duckdb/duckdb/tree/%s\n\nConservative source inventory includes DuckDB LICENSE, all third_party and extension notices. These native notices are additional to Go module licenses. Runtime-downloaded extensions are not part of the release archive and retain their own licensing.\n\n", duckdbVersion, duckdbCommit)
	for _, dir := range []string{".", "third_party", "extension"} {
		var ns []notice
		if dir == "." {
			data, err := os.ReadFile(filepath.Join(source, "LICENSE"))
			if err != nil {
				return nil, err
			}
			ns = []notice{{"LICENSE", string(data)}}
		} else {
			var err error
			ns, err = collect(filepath.Join(source, dir))
			if err != nil {
				return nil, err
			}
		}
		for _, n := range ns {
			if dir != "." {
				n.name = dir + "/" + n.name
			}
			if err := emit(&out, n); err != nil {
				return nil, fmt.Errorf("DuckDB %s: %w", n.name, err)
			}
		}
	}
	return out.Bytes(), nil
}

func appendNative(out *bytes.Buffer, root string) error {
	for path, version := range map[string]string{"github.com/duckdb/duckdb-go/v2": "v2.10502.0", "github.com/duckdb/duckdb-go-bindings": "v0.10502.0"} {
		actual, err := command(root, nil, "list", "-m", "-f", "{{.Version}}", path)
		if err != nil {
			return err
		}
		if string(bytes.TrimSpace(actual)) != version {
			return fmt.Errorf("%s changed: regenerate and review native DuckDB source notices", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "scripts/licenses/duckdb-notices.md"))
	if err != nil {
		return err
	}
	// Pin the reviewed snapshot. Refresh only after reviewing every native license
	// and matching the engine source version to the bindings module.
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != nativeNoticesSHA256 {
		return fmt.Errorf("native DuckDB notice snapshot changed, review and update its SHA-256: %s", got)
	}
	out.Write(data)
	return nil
}

const nativeNoticesSHA256 = "793cb9fa24b91b25133a05913813de23357f424c7ef6cc61cc71df182f3f2d95"
