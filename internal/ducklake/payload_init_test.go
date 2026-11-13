package ducklake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/duckdb/duckdb-go/v2"
)

func TestPayloadInitTransient(t *testing.T) {
	for _, tc := range []struct {
		typ   duckdb.ErrorType
		retry bool
	}{
		{duckdb.ErrorTypeTransaction, true},
		{duckdb.ErrorTypeCatalog, false},
		{duckdb.ErrorTypeConnection, false},
		{duckdb.ErrorTypeIO, false},
		{duckdb.ErrorTypeConstraint, false},
	} {
		err := fmt.Errorf("wrapped: %w", &duckdb.Error{Type: tc.typ})
		if payloadInitTransient(err) != tc.retry {
			t.Fatalf("type %v retry mismatch", tc.typ)
		}
	}
	if payloadInitTransient(errors.New("transaction conflict")) {
		t.Fatal("untyped diagnostic retried")
	}
}

// A plain embedded catalog with the same options macro exercises the fast path
// without any extension, network, or mutable DuckLake catalog operations.
func TestPayloadInitConfiguredReadOnly(t *testing.T) {
	lake := embeddedPayloadLake(t)
	ctx := context.Background()
	if err := lake.Exec(ctx, `CREATE MACRO lake.main.options() AS TABLE SELECT 'data_inlining_row_limit' AS option_name, '0' AS value, 'TABLE' AS scope, 'main.looster_payloads' AS scope_entry`); err != nil {
		t.Fatal(err)
	}
	if err := initializePayloadTable(ctx, lake); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := initializePayloadTable(canceled, lake); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestPayloadInitRequiresExplicitTableZero(t *testing.T) {
	for _, tc := range []struct{ name, scope, entry, value string }{
		{"global", "GLOBAL", "", "0"},
		{"schema", "SCHEMA", "main", "0"},
		{"other-table", "TABLE", "main.other", "0"},
		{"nonzero", "TABLE", "main.looster_payloads", "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lake := embeddedPayloadLake(t)
			query := "CREATE MACRO lake.main.options() AS TABLE SELECT 'data_inlining_row_limit' AS option_name, " + quote(tc.value) + " AS value, " + quote(tc.scope) + " AS scope, " + quote(tc.entry) + " AS scope_entry"
			if err := lake.Exec(context.Background(), query); err != nil {
				t.Fatal(err)
			}
			// This mock catalog deliberately has no set_option macro. Only an
			// explicit table zero may take the read-only success path.
			if err := initializePayloadTable(context.Background(), lake); err == nil {
				t.Fatal("nonmatching option incorrectly accepted as configured")
			}
		})
	}
}

func cachedPayloadInitLake(t *testing.T) (*Lake, string) {
	t.Helper()
	ctx := context.Background()
	lake, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lake.Close() })
	var platform string
	if err = lake.withConn(ctx, "platform", func(ctx context.Context, conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, "PRAGMA platform").Scan(&platform)
	}); err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(cache, "looster", "duckdb", EngineVersion, platform, "extensions", "ducklake.duckdb_extension")
	if _, err = os.Stat(extension); os.IsNotExist(err) {
		t.Skip("signed DuckLake extension not cached")
	} else if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	attach := "LOAD " + quote(extension) + "; ATTACH " + quote("ducklake:"+filepath.Join(root, "catalog.ducklake")) + " AS lake (DATA_PATH " + quote(filepath.Join(root, "data")) + ")"
	if err = lake.Exec(ctx, attach); err != nil {
		t.Fatal(err)
	}
	return lake, attach
}

func payloadInitSnapshotCount(t *testing.T, lake *Lake) int {
	t.Helper()
	var count int
	if err := lake.withConn(context.Background(), "snapshot count", func(ctx context.Context, conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, "SELECT count(*) FROM lake.snapshots()").Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPayloadInitCachedDuckLakeReopen(t *testing.T) {
	lake, attach := cachedPayloadInitLake(t)
	ctx := context.Background()
	store, err := NewPayloadStore(ctx, lake, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "preserved", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	before := payloadInitSnapshotCount(t, lake)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = reader.Exec(ctx, attach); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPayloadStore(ctx, reader, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if after := payloadInitSnapshotCount(t, reader); after != before {
		t.Fatalf("reopen created snapshots: %d -> %d", before, after)
	}
	got, err := reopened.Get(ctx, "preserved")
	if err != nil || string(got) != "payload" {
		t.Fatalf("existing payload not preserved: %v", err)
	}
}

func TestPayloadInitCachedDuckLakeExistingLayout(t *testing.T) {
	lake, _ := cachedPayloadInitLake(t)
	ctx := context.Background()
	if err := lake.Exec(ctx, `CREATE TABLE lake.main.looster_payloads(payload_key VARCHAR, payload BLOB); ALTER TABLE lake.main.looster_payloads SET PARTITIONED BY (bucket(8, payload_key)); INSERT INTO lake.main.looster_payloads VALUES ('old', 'data'::BLOB)`); err != nil {
		t.Fatal(err)
	}
	before := payloadInitSnapshotCount(t, lake)
	if err := initializePayloadTable(ctx, lake); err != nil {
		t.Fatal(err)
	}
	if after := payloadInitSnapshotCount(t, lake); after != before {
		t.Fatalf("existing layout mutated: %d -> %d", before, after)
	}
	// A second call must also avoid re-setting the now-correct explicit option.
	if err := initializePayloadTable(ctx, lake); err != nil {
		t.Fatal(err)
	}
	if after := payloadInitSnapshotCount(t, lake); after != before {
		t.Fatal("configured reopen mutated catalog")
	}
	got, err := (lakePayloadSink{lake}).get(ctx, "old")
	if err != nil || string(got) != "data" {
		t.Fatalf("existing data not preserved: %v", err)
	}
}
