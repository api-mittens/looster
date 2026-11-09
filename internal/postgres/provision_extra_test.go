package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConfigureRepeatExistingAndCancellation(t *testing.T) {
	clearPGEnvironment(t)
	f := &provisionFake{exists: map[string]bool{"app": true, "catalog": true}, execErr: &pgconn.PgError{Code: "42501", Message: "secret"}}
	migrations := 0
	connect := func(context.Context, *pgx.ConnConfig) (provisioningConn, error) { return f, nil }
	migrate := func(context.Context, string) error { migrations++; return nil }
	for i := 0; i < 2; i++ {
		if _, err := configureDatabases(context.Background(), "dbname=app", "", "catalog", connect, migrate); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.statements) != 0 || migrations != 2 {
		t.Fatal("repeat provisioning requires database creation or skips migration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := configureDatabases(ctx, "dbname=app", "", "", func(context.Context, *pgx.ConnConfig) (provisioningConn, error) { return nil, errors.New("secret") }, migrate)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestConfigureInvalidSelectionNeverConnects(t *testing.T) {
	clearPGEnvironment(t)
	for _, tc := range []struct{ raw, app, catalog string }{
		{raw: "postgres://%invalid"},
		{raw: "dbname=app", catalog: "app"},
		{app: strings.Repeat("a", 64)},
	} {
		_, err := configureDatabases(context.Background(), tc.raw, tc.app, tc.catalog, func(context.Context, *pgx.ConnConfig) (provisioningConn, error) {
			t.Fatal("connected before validation")
			return nil, nil
		}, func(context.Context, string) error { t.Fatal("migrated before validation"); return nil })
		if err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
