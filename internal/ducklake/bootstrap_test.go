package ducklake

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifiedEngine(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, err := z.Create("duckdb")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("verified-engine"))
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b.Bytes())
	digest := hex.EncodeToString(h[:])
	got, err := verifiedEngine(b.Bytes(), digest)
	if err != nil || string(got) != "verified-engine" {
		t.Fatalf("extract: %q %v", got, err)
	}
	tampered := append([]byte{}, b.Bytes()...)
	tampered[len(tampered)-1] ^= 1
	if _, err = verifiedEngine(tampered, digest); err == nil {
		t.Fatal("tampered archive accepted")
	}
	if _, err = verifiedEngine(b.Bytes(), strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong pin accepted")
	}
}
func TestAtomicPrivateWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "duckdb")
	for _, v := range []string{"old", "new"} {
		if err := atomicPrivateWrite(p, []byte(v), 0700); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "new" {
		t.Fatal("replacement failed")
	}
	s, err := os.Stat(p)
	if err != nil || s.Mode().Perm() != 0700 {
		t.Fatal("executable permissions not private")
	}
}
func TestNamespaceMetadataIsolation(t *testing.T) {
	a := config()
	a.Namespace = "tenant-a"
	b := a
	b.Namespace = "tenant_a"
	sa, err := prepare(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := prepare(b)
	if err != nil {
		t.Fatal(err)
	}
	if metadataSchema(a.Namespace) == metadataSchema(b.Namespace) || sa == sb {
		t.Fatal("namespace collision")
	}
	if !strings.Contains(sa, "METADATA_SCHEMA '"+metadataSchema(a.Namespace)+"'") {
		t.Fatal("missing schema catalog")
	}
	if strings.Contains(sa, "CREATE DATABASE") {
		t.Fatal("database provisioning in catalog attach")
	}
}

// This test downloads only public engine/extension artifacts into a fresh local
// cache. It does not contact any PostgreSQL or S3 service.
func TestEngineBootstrapArtifacts(t *testing.T) {
	if os.Getenv("LOOSTER_TEST_ENGINE_BOOTSTRAP") != "yes" {
		t.Skip("opt-in public artifact download")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary, err := acquireEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, []byte("unverified cache"), 0700); err != nil {
		t.Fatal(err)
	}
	restored, err := acquireEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restoredBytes, err := os.ReadFile(restored)
	if err != nil || !bytes.Equal(original, restoredBytes) {
		t.Fatal("cached executable not reconstructed from verified archive")
	}
	install, err := extensionInstallSQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sql := install + " SELECT CASE WHEN version()='" + EngineVersion + "' AND (SELECT count(*) FROM duckdb_extensions() WHERE loaded AND ((extension_name='ducklake' AND extension_version='" + DuckLakeExtensionVersion + "') OR (extension_name='postgres_scanner' AND extension_version='" + PostgresExtensionVersion + "') OR (extension_name='httpfs' AND extension_version='" + HTTPFSExtensionVersion + "'))) = 3 THEN true ELSE error('pin mismatch') END;"
	cmd := exec.CommandContext(ctx, binary, "-init", "/dev/null", "-batch", "-bail", ":memory:")
	cmd.Stdin = strings.NewReader(sql)
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH")}
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("public artifact install failed: %v %s", e, output)
	}
}
