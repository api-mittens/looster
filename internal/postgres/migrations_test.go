package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Validate the embedded migration catalog even when integration tests have no
// database configured. Independently merged migrations must not reuse versions.
func TestEmbeddedMigrationCatalog(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://localhost:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	// Construction discovers migrations without opening a database connection.
	if _, err := goose.NewProvider(goose.DialectPostgres, db, mustSub()); err != nil {
		t.Fatalf("invalid embedded migration catalog: %v", err)
	}
}
