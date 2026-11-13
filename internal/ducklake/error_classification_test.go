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
		})
	}
	for _, cause := range []error{nil, errors.New(secret + " Out of Memory Error: s3://private AccessDenied"), (*duckdb.Error)(nil)} {
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
}
