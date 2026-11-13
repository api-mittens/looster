package ducklake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func TestObservedCatalogFailuresAreRetryable(t *testing.T) {
	for _, tc := range []struct {
		typ duckdb.ErrorType
		msg string
	}{
		{duckdb.ErrorTypeInvalid, "could not serialize access due to concurrent update password=CANARY"},
		{duckdb.ErrorTypeIO, "remaining connection slots are reserved for roles with the SUPERUSER attribute password=CANARY"},
		{duckdb.ErrorTypeIO, "server closed the connection unexpectedly password=CANARY"},
	} {
		cause := &duckdb.Error{Type: tc.typ, Msg: tc.msg}
		err := classifiedOperationError("payload transaction", cause)
		if !errors.Is(err, ErrCatalogBusy) || !payloadInitTransient(cause) {
			t.Fatalf("missing retry identity: %v", err)
		}
		if strings.Contains(err.Error(), "CANARY") || errors.Unwrap(err) != nil {
			t.Fatal("native diagnostic escaped")
		}
	}
}

func TestPayloadCatalogBusyRetainsWaitersAndDurableRecord(t *testing.T) {
	busy := true
	sink := payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error {
		if busy {
			return fmt.Errorf("safe catalog retry: %w", ErrCatalogBusy)
		}
		return nil
	}}
	s := payloadManualStore(t, sink)
	waiter := payloadQueueTestRecord(t, s, "pending")
	for i := 0; i < 3; i++ {
		if err := s.flushBatches(context.Background(), time.Minute); !errors.Is(err, ErrCatalogBusy) {
			t.Fatalf("attempt %d: %v", i, err)
		}
		payloadAssertWaiting(t, waiter)
		if _, err := os.Stat(filepath.Join(s.dir, payloadName("pending"))); err != nil {
			t.Fatal("retry lost durable record:", err)
		}
	}
	busy = false
	if err := s.flushBatches(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	waitPayloadResult(t, waiter)
	if _, err := os.Stat(filepath.Join(s.dir, payloadName("pending"))); !os.IsNotExist(err) {
		t.Fatal("committed spool retained:", err)
	}
}

func TestPayloadExplicitFlushReportsCatalogBusy(t *testing.T) {
	sink := payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error { return ErrCatalogBusy }}
	s := payloadManualStore(t, sink)
	waiter := payloadQueueTestRecord(t, s, "pending")
	if err := s.flushBatches(context.Background(), 0); !errors.Is(err, ErrCatalogBusy) {
		t.Fatal(err)
	}
	select {
	case err := <-waiter:
		if !errors.Is(err, ErrCatalogBusy) {
			t.Fatal(err)
		}
	default:
		t.Fatal("explicit flush hid failure")
	}
	if _, err := os.Stat(filepath.Join(s.dir, payloadName("pending"))); err != nil {
		t.Fatal(err)
	}
}

func requireSQLBefore(t *testing.T, sql, before, after string) {
	t.Helper()
	a, b := strings.Index(sql, before), strings.Index(sql, after)
	if a < 0 || b < 0 || a >= b {
		t.Fatalf("missing or incorrectly ordered setup statements %q and %q", before, after)
	}
}

func TestMetadataPoolDoesNotRetainConnections(t *testing.T) {
	prelude, err := prepare(config())
	if err != nil {
		t.Fatal(err)
	}
	requireSQLBefore(t, prelude, "LOAD postgres;", "SET pg_pool_max_connections=0;")
	for _, setting := range []string{"SET pg_pool_max_connections=0;", "SET pg_pool_enable_thread_local_cache=false;"} {
		requireSQLBefore(t, prelude, setting, "ATTACH 'ducklake:looster_lake'")
	}
	requireSQLBefore(t, prelude, "ATTACH 'ducklake:looster_lake'", "FROM postgres_configure_pool(catalog_name='__ducklake_metadata_lake', max_connections=0, enable_thread_local_cache=false)")
}
