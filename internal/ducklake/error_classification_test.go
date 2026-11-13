package ducklake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

func TestClassifiedOperationError(t *testing.T) {
	const secret = "CANARY-password=top-secret AWS_SECRET_ACCESS_KEY=secret-token https://user:pass@host/?X-Amz-Signature=private"
	tests := []struct {
		name              string
		typ               duckdb.ErrorType
		message, category string
	}{
		{"oom", duckdb.ErrorTypeOutOfMemory, "", "out_of_memory"},
		{"conflict", duckdb.ErrorTypeTransaction, "Conflict on update", "transaction_conflict"},
		{"retry", duckdb.ErrorTypeTransaction, "Exceeded the maximum retry count of 10", "transaction_retry_exhausted"},
		{"invalid serialization", duckdb.ErrorTypeInvalid, "could not serialize access due to concurrent update", "transaction_conflict"},
		{"invalid alter", duckdb.ErrorTypeInvalid, "Transaction conflict attempting to alter table index1 another transaction altered it", "transaction_conflict"},
		{"invalid retry", duckdb.ErrorTypeInvalid, "Exceeded the maximum retry count of 10", "transaction_retry_exhausted"},
		{"reserved slots", duckdb.ErrorTypeInvalid, "FATAL remaining connection slots are reserved for roles with SUPERUSER attribute", "connection_limit"},
		{"fatal reserved slots", duckdb.ErrorTypeFatal, "remaining connection slots are reserved", "connection_limit"},
		{"too many clients", duckdb.ErrorTypeIO, "sorry, too many clients already", "connection_limit"},
		{"role limit", duckdb.ErrorTypeConnection, "too many connections for role private_role", "connection_limit"},
		{"database limit", duckdb.ErrorTypeInvalid, "too many connections for database private_db", "connection_limit"},
		{"dependency serialization", duckdb.ErrorTypeInvalid, "could not serialize access due to read/write dependencies among transactions", "transaction_conflict"},
		{"s3 forbidden", duckdb.ErrorTypeHTTP, "HTTP GET s3://private/key 403 (Forbidden)", "s3_authorization"},
		{"s3 denied", duckdb.ErrorTypeIO, "s3://private/key AccessDenied", "s3_authorization"},
		{"s3 signature", duckdb.ErrorTypeHTTP, "s3://private/key SignatureDoesNotMatch", "s3_authorization"},
		{"s3 network", duckdb.ErrorTypeIO, "s3://private/key connection reset", "s3_network"},
		{"catalog disconnect", duckdb.ErrorTypeIO, "server closed the connection unexpectedly", "catalog_disconnect"},
		{"connection", duckdb.ErrorTypeConnection, "", "catalog_or_engine_disconnect"},
		{"permission", duckdb.ErrorTypePermission, "", "authorization"},
		{"network", duckdb.ErrorTypeNetwork, "", "network"},
		{"catalog", duckdb.ErrorTypeCatalog, "", "catalog"},
		{"http", duckdb.ErrorTypeHTTP, "", "http"},
		{"io", duckdb.ErrorTypeIO, "", "io"},
		{"constraint", duckdb.ErrorTypeConstraint, "", "constraint"},
		{"interrupt", duckdb.ErrorTypeInterrupt, "", "interrupted"},
		{"fatal", duckdb.ErrorTypeFatal, "", "engine_internal"},
		{"internal", duckdb.ErrorTypeInternal, "", "engine_internal"},
		{"configuration", duckdb.ErrorTypeInvalidConfiguration, "", "configuration"},
		{"unknown", duckdb.ErrorType(9999), "out of memory s3://private AccessDenied", ""},
		{"parser", duckdb.ErrorTypeParser, "Exceeded the maximum retry count", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			native := &duckdb.Error{Type: tt.typ, Msg: tt.message + " " + secret}
			cause := fmt.Errorf("%s: %w", secret, native)
			got := classifiedOperationError("payload commit", cause)
			want := operationError("payload commit").Error()
			if tt.category != "" {
				want += " [category: " + tt.category + "]"
			}
			if got.Error() != want {
				t.Fatalf("unexpected sanitized output: %q", got)
			}
			if strings.Contains(got.Error(), secret) || errors.Unwrap(got) != nil || errors.Is(got, native) {
				t.Fatal("native diagnostic escaped")
			}
			var exposed *duckdb.Error
			if errors.As(got, &exposed) {
				t.Fatal("native cause exposed")
			}
			busy := tt.category == "connection_limit" || tt.category == "transaction_conflict" || tt.category == "transaction_retry_exhausted" || tt.category == "catalog_disconnect"
			if errors.Is(got, ErrCatalogBusy) != busy || errors.Is(fmt.Errorf("outer: %w", got), ErrCatalogBusy) != busy {
				t.Fatal("incorrect safe retry identity")
			}
		})
	}
	for _, cause := range []error{nil, errors.New(secret + " Out of Memory Error: s3://private AccessDenied could not serialize access due to concurrent update remaining connection slots are reserved"), (*duckdb.Error)(nil)} {
		if got := classifiedOperationError("payload commit", cause); got.Error() != operationError("payload commit").Error() {
			t.Fatalf("untyped error classified: %q", got)
		}
	}
}

func TestWithConnClassificationContextIdentity(t *testing.T) {
	newLake := func() *Lake {
		lifetime, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return &Lake{gate: make(chan struct{}, 1), lifetime: lifetime, cancel: cancel}
	}
	native := &duckdb.Error{Type: duckdb.ErrorTypeOutOfMemory, Msg: "CANARY native password"}
	t.Run("classified callback", func(t *testing.T) {
		lake := newLake()
		got := lake.withConn(context.Background(), "payload commit", func(context.Context, *sql.Conn) error { return native })
		if got.Error() != classifiedOperationError("payload commit", native).Error() {
			t.Fatalf("unexpected error: %v", got)
		}
	})
	t.Run("callback cancellation", func(t *testing.T) {
		lake := newLake()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got := lake.withConn(ctx, "payload commit", func(context.Context, *sql.Conn) error { cancel(); return native })
		if got != context.Canceled {
			t.Fatalf("lost cancellation identity: %v", got)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		got := newLake().withConn(ctx, "payload commit", func(context.Context, *sql.Conn) error { t.Fatal("callback called"); return nil })
		if got != context.DeadlineExceeded {
			t.Fatalf("lost deadline identity: %v", got)
		}
	})
	t.Run("closed during callback", func(t *testing.T) {
		lake := newLake()
		got := lake.withConn(context.Background(), "payload commit", func(context.Context, *sql.Conn) error { lake.cancel(); return native })
		if got != ErrClosed {
			t.Fatalf("lost closed identity: %v", got)
		}
	})
}

func TestWithConnNativeDriverClassification(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	lake := &Lake{gate: make(chan struct{}, 1), lifetime: lifetime, cancel: cancel, conn: conn}
	got := lake.withConn(context.Background(), "payload commit", func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `SELECT * FROM CANARY_private_catalog_password`)
		var native *duckdb.Error
		if !errors.As(err, &native) || native.Type != duckdb.ErrorTypeCatalog {
			t.Fatalf("expected native catalog error, got %T", err)
		}
		return err
	})
	want := operationError("payload commit").Error() + " [category: catalog]"
	if got == nil || got.Error() != want || errors.Unwrap(got) != nil {
		t.Fatalf("unexpected native boundary result: %v", got)
	}
	for _, method := range []string{"exec", "query", "query callback"} {
		t.Run(method, func(t *testing.T) {
			stage, category := "SQL query", "catalog"
			var got error
			switch method {
			case "exec":
				stage = "SQL operation"
				got = lake.Exec(context.Background(), `SELECT * FROM CANARY_private_catalog_password`)
			case "query":
				got = lake.Query(context.Background(), `SELECT * FROM CANARY_private_catalog_password`, func(*sql.Rows) error {
					t.Fatal("callback called on failed query")
					return nil
				})
			case "query callback":
				category = "transaction_conflict"
				got = lake.Query(context.Background(), `SELECT 1`, func(*sql.Rows) error {
					return &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "could not serialize access due to concurrent update CANARY_private_password"}
				})
				if !errors.Is(got, ErrCatalogBusy) {
					t.Fatal("query lost safe retry identity")
				}
			}
			want := operationError(stage).Error() + " [category: " + category + "]"
			if got == nil || got.Error() != want || errors.Unwrap(got) != nil {
				t.Fatalf("unexpected sanitized result: %v", got)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := lake.Exec(ctx, "SELECT 1"); err != context.Canceled {
				t.Fatal("Exec lost context identity")
			}
			if err := lake.Query(ctx, "SELECT 1", func(*sql.Rows) error { return nil }); err != context.Canceled {
				t.Fatal("Query lost context identity")
			}
		})
	}
}
