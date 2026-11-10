package ducklake

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This additional integration check uses only an already cached signed artifact.
// The mandatory embedded tests above need no extensions or external services.
func TestPayloadEmbeddedDuckLakeParquet(t *testing.T) {
	ctx := context.Background()
	lake, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lake.Close()
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
	catalog := filepath.Join(root, "catalog.ducklake")
	attach := "LOAD " + quote(extension) + "; ATTACH " + quote("ducklake:"+catalog) + " AS lake (DATA_PATH " + quote(filepath.Join(root, "data")) + ")"
	if err = lake.Exec(ctx, attach); err != nil {
		t.Fatal(err)
	}
	store, err := NewPayloadStore(ctx, lake, PayloadOptions{SpoolDir: filepath.Join(root, "spool"), BatchBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := bytes.Repeat([]byte{0, 255, 39, 10}, (5<<20)/4)
	if err = store.Put(ctx, "parquet-large", value); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "second-batch", []byte{}); err != nil {
		t.Fatal(err)
	}
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
	got, err := (lakePayloadSink{reader}).get(ctx, "parquet-large")
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("fresh DuckLake reader bytes=%d err=%v", len(got), err)
	}
	var files int
	err = filepath.WalkDir(filepath.Join(root, "data"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".parquet" {
			files++
		}
		return nil
	})
	if err != nil || files == 0 {
		t.Fatalf("payload was not parquet-backed: files=%d err=%v", files, err)
	}
}

func embeddedPayloadLake(t *testing.T) *Lake {
	t.Helper()
	lake, err := OpenEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lake.Close() })
	if err = lake.Exec(context.Background(), "ATTACH ':memory:' AS lake; CREATE TABLE lake.main.looster_payloads(payload_key VARCHAR CHECK(payload_key <> 'reject'), payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	return lake
}

func TestPayloadEmbeddedAppenderChunksAndRollback(t *testing.T) {
	lake := embeddedPayloadLake(t)
	sink := lakePayloadSink{lake}
	ctx := context.Background()
	records := make([]payloadRecord, 4097)
	keys := make([]string, len(records))
	for i := range records {
		keys[i] = fmt.Sprintf("chunk-%d", i)
		records[i] = payloadRecord{keys[i], []byte{byte(i), 0, 255}}
	}
	if err := sink.putBatch(ctx, records); err != nil {
		t.Fatal(err)
	}
	rows, err := sink.getMany(ctx, keys)
	if err != nil || len(rows) != len(records) {
		t.Fatalf("chunked rows=%d err=%v", len(rows), err)
	}
	for _, r := range records {
		if string(rows[r.Key]) != string(r.Value) {
			t.Fatalf("corrupt %s", r.Key)
		}
	}
	if err = sink.putBatch(ctx, []payloadRecord{{"rolled-back", []byte("first")}, {"reject", []byte("constraint")}}); err == nil {
		t.Fatal("constraint accepted")
	}
	if _, err = sink.get(ctx, "rolled-back"); !errors.Is(err, ErrPayloadNotFound) {
		t.Fatalf("partial append committed: %v", err)
	}
	if err = sink.putBatch(ctx, []payloadRecord{{"after-failure", nil}}); err != nil {
		t.Fatalf("retained transaction not cleaned: %v", err)
	}
	if err = sink.putBatch(ctx, []payloadRecord{{"duplicate", []byte("a")}, {"duplicate", []byte("b")}}); !errors.Is(err, ErrPayloadConflict) {
		t.Fatalf("in-batch conflict: %v", err)
	}
	if err = sink.putBatch(ctx, []payloadRecord{{"after-conflict", []byte("ok")}}); err != nil {
		t.Fatal(err)
	}
}

func TestPayloadEmbeddedCancellationWhileQueued(t *testing.T) {
	lake := embeddedPayloadLake(t)
	sink := lakePayloadSink{lake}
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- lake.withConn(context.Background(), "test gate", func(context.Context, *sql.Conn) error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := sink.putBatch(ctx, []payloadRecord{{"canceled", []byte("never")}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("queued cancellation: %v", err)
	}
	if _, err := sink.getMany(ctx, []string{"canceled"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("query cancellation: %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := sink.putBatch(context.Background(), []payloadRecord{{"healthy", []byte("ok")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.get(context.Background(), "canceled"); !errors.Is(err, ErrPayloadNotFound) {
		t.Fatalf("canceled append persisted: %v", err)
	}
}
