package ducklake

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifiedWindowsExecutable(t *testing.T) {
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	w, err := z.Create("duckdb.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("verified-windows-engine")); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data.Bytes())
	got, err := verifiedEngine(data.Bytes(), hex.EncodeToString(h[:]))
	if err != nil || string(got) != "verified-windows-engine" {
		t.Fatalf("Windows archive extraction: %v", err)
	}
}
func TestPreparedAbsoluteExtensionLoads(t *testing.T) {
	c := config()
	c.extensions = "SET autoinstall_known_extensions=false; SET autoload_known_extensions=false; LOAD '/private/cache/httpfs.duckdb_extension'; LOAD '/private/cache/postgres_scanner.duckdb_extension'; LOAD '/private/cache/ducklake.duckdb_extension';"
	sql, err := prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, c.extensions) {
		t.Fatal("bootstrap artifact paths not retained for reattach")
	}
	for _, bad := range []string{"INSTALL ", "LOAD ducklake;", "LOAD postgres;", "LOAD httpfs;", "FORCE INSTALL"} {
		if strings.Contains(sql, bad) {
			t.Fatalf("uses ambient extension installation: %s", bad)
		}
	}
}
