package ducklake

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

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
		t.Fatal("missing schema isolation")
	}
	if strings.Contains(sa, "CREATE DATABASE") {
		t.Fatal("catalog attach provisions database")
	}
}

func TestCatalogIdentityRotation(t *testing.T) {
	a := config()
	first, err := CatalogIdentity(a)
	if err != nil {
		t.Fatal(err)
	}
	a.CatalogConnection += " password=rotated sslrootcert=/new/root"
	rotated, err := CatalogIdentity(a)
	if err != nil || first != rotated {
		t.Fatal("credential rotation changed spool identity")
	}
	a.Namespace = "different"
	other, err := CatalogIdentity(a)
	if err != nil || first == other {
		t.Fatal("namespace not isolated")
	}
}

func TestEmbeddedQuery(t *testing.T) {
	ctx := context.Background()
	lake, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lake.Close()
	if err = lake.Exec(ctx, "CREATE TEMP TABLE result (value BLOB); INSERT INTO result VALUES (from_hex('00ff'))"); err != nil {
		t.Fatal(err)
	}
	err = lake.Query(ctx, "SELECT value FROM result", func(rows *sql.Rows) error {
		if !rows.Next() {
			t.Fatal("missing retained session row")
		}
		var got []byte
		if err := rows.Scan(&got); err != nil {
			return err
		}
		if string(got) != string([]byte{0, 255}) {
			t.Fatalf("bad binary value %x", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	canary := "secret-value-canary"
	err = lake.Query(ctx, "SELECT 1", func(*sql.Rows) error { return errors.New(canary) })
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("callback error not sanitized: %v", err)
	}
	if err = lake.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal("query did not release session", err)
	}
	if err = lake.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lake.Query(ctx, "SELECT 1", func(*sql.Rows) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed query: %v", err)
	}
}
