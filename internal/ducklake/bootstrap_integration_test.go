package ducklake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/objectstore"
	"github.com/api-mittens/looster/internal/postgres"
)

// Requires explicit, test-only infrastructure. Uses a unique metadata namespace
// and S3 prefix, never expires snapshots or performs catalog-wide cleanup.
func TestIsolatedBootstrapIntegration(t *testing.T) {
	raw := os.Getenv("LOOSTER_TEST_BOOTSTRAP_PG")
	if raw == "" {
		t.Skip("requires explicit isolated bootstrap PG/S3 services")
	}
	for _, key := range []string{"LOOSTER_TEST_BOOTSTRAP_S3_ENDPOINT", "LOOSTER_TEST_BOOTSTRAP_S3_BUCKET", "LOOSTER_TEST_BOOTSTRAP_S3_ACCESS_KEY", "LOOSTER_TEST_BOOTSTRAP_S3_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("missing test-only setting %s", key)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, err := postgres.DatabaseConnectionString(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("bootstrap_%x", time.Now().UnixNano())
	cfg := Config{CatalogConnection: conn, Namespace: ns, DataPath: "s3://" + os.Getenv("LOOSTER_TEST_BOOTSTRAP_S3_BUCKET") + "/bootstrap-tests/" + ns + "/", S3: objectstore.Config{
		Endpoint: os.Getenv("LOOSTER_TEST_BOOTSTRAP_S3_ENDPOINT"), Bucket: os.Getenv("LOOSTER_TEST_BOOTSTRAP_S3_BUCKET"), Region: "us-east-1", AccessKey: os.Getenv("LOOSTER_TEST_BOOTSTRAP_S3_ACCESS_KEY"), SecretKey: os.Getenv("LOOSTER_TEST_BOOTSTRAP_S3_SECRET_KEY"),
	}}
	lake, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("automatic Open: %v", err)
	}
	store, err := NewPayloadStore(ctx, lake, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("isolated bootstrap durable payload\x00\xff")
	if err = store.Put(ctx, "bootstrap-roundtrip", payload); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err = store.Flush(ctx); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	// A new Lake and an empty spool prove retrieval from actual Parquet storage.
	reopened, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewPayloadStore(ctx, reopened, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	got, err := reader.Get(ctx, "bootstrap-roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("reopened payload mismatch")
	}
	// A distinct namespace must not observe the first namespace's payload.
	other := cfg
	other.Namespace = ns + "_other"
	other.DataPath = cfg.DataPath + "other/"
	otherLake, err := Open(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	otherStore, err := NewPayloadStore(ctx, otherLake, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := otherStore.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err = otherStore.Get(ctx, "bootstrap-roundtrip"); !errors.Is(err, ErrPayloadNotFound) {
		t.Fatalf("namespace payload leakage or unexpected read failure: %v", err)
	}
}
