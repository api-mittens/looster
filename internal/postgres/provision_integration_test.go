package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This opt-in gate must name a disposable cluster whose role can create/drop
// test databases and roles. No existing application database is migrated.
func provisioningIntegration(t *testing.T) (context.Context, string, *pgx.Conn, string) {
	t.Helper()
	raw := os.Getenv("LOOSTER_TEST_PROVISION_DATABASE_URL")
	if raw == "" {
		t.Skip("set LOOSTER_TEST_PROVISION_DATABASE_URL to a disposable PostgreSQL cluster")
	}
	clearPGEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cfg, err := parsePoolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })
	return ctx, raw, admin, fmt.Sprintf("looster_provision_%d", time.Now().UnixNano())
}

func cleanupProvisionDatabases(t *testing.T, admin *pgx.Conn, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Errorf("cleanup disposable database: %v", err)
			}
		}
	})
}

func provisionTestConn(t *testing.T, ctx context.Context, raw string) *pgx.Conn {
	t.Helper()
	cfg, err := parsePoolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func TestProvisionIntegrationCustomRepeatNamespace(t *testing.T) {
	ctx, raw, admin, prefix := provisioningIntegration(t)
	app, catalog := prefix+`_custom"db`, prefix+"_catalog"
	cleanupProvisionDatabases(t, admin, app, catalog)
	selected, err := ConfigureDatabasesNamespace(ctx, raw, app, catalog, "team")
	if err != nil {
		t.Fatal(err)
	}
	conn := provisionTestConn(t, ctx, selected)
	if _, err := conn.Exec(ctx, "CREATE TABLE public.provision_sentinel (value text); INSERT INTO public.provision_sentinel VALUES ('retained')"); err != nil {
		t.Fatal(err)
	}
	var firstVersions int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM looster_ns_team.goose_db_version").Scan(&firstVersions); err != nil {
		t.Fatal(err)
	}
	// No override on repeat: custom selected DB must remain selected.
	again, err := ConfigureDatabasesNamespace(ctx, selected, "", catalog, "team")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parsePoolConfig(again)
	if err != nil || cfg.ConnConfig.Database != app {
		t.Fatal("custom database was replaced")
	}
	var sentinel string
	if err := conn.QueryRow(ctx, "SELECT value FROM public.provision_sentinel").Scan(&sentinel); err != nil || sentinel != "retained" {
		t.Fatalf("sentinel lost: %v", err)
	}
	var versions int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM looster_ns_team.goose_db_version").Scan(&versions); err != nil || versions != firstVersions {
		t.Fatalf("migration history changed on repeat: %v", err)
	}
	var publicVersion bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.goose_db_version') IS NOT NULL").Scan(&publicVersion); err != nil || publicVersion {
		t.Fatalf("nondefault namespace leaked into public: %v", err)
	}
	catalogRaw, _, err := selectDatabase(raw, catalog, "")
	if err != nil {
		t.Fatal(err)
	}
	catalogConn := provisionTestConn(t, ctx, catalogRaw)
	if err := catalogConn.QueryRow(ctx, "SELECT to_regclass('public.goose_db_version') IS NOT NULL").Scan(&publicVersion); err != nil || publicVersion {
		t.Fatalf("catalog migrated as application: %v", err)
	}
}

func TestProvisionIntegrationConcurrent(t *testing.T) {
	ctx, raw, admin, prefix := provisioningIntegration(t)
	app, catalog := prefix+"_app", prefix+"_catalog"
	cleanupProvisionDatabases(t, admin, app, catalog)
	const runners = 3
	errs := make(chan error, runners)
	var wg sync.WaitGroup
	for i := 0; i < runners; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := ConfigureDatabases(ctx, raw, app, catalog); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	selected, _, err := selectDatabase(raw, app, "")
	if err != nil {
		t.Fatal(err)
	}
	conn := provisionTestConn(t, ctx, selected)
	var versions int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM public.goose_db_version").Scan(&versions); err != nil || versions < 2 {
		t.Fatalf("application migrations missing: %v", err)
	}
}

func provisionRoleConnection(raw, role, database string) (string, error) {
	selected, _, err := selectDatabase(raw, database, "")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(selected, "postgres://") || strings.HasPrefix(selected, "postgresql://") {
		u, err := url.Parse(selected)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("user", role)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return selected + " user='" + role + "'", nil
}

func TestProvisionIntegrationExistingWithoutCreatedb(t *testing.T) {
	ctx, raw, admin, prefix := provisioningIntegration(t)
	role, app, missing := prefix+"_role", prefix+"_app", prefix+"_missing"
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOCREATEDB"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Errorf("cleanup role: %v", err)
		}
	})
	cleanupProvisionDatabases(t, admin, app, missing)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{app}.Sanitize()+" OWNER "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	// Keyword user override retains all server and TLS settings. The disposable
	// cluster must allow this test login (e.g. local trust authentication).
	// URL connections instead use an explicit user query override.
	restricted, err := provisionRoleConnection(raw, role, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigureDatabases(ctx, restricted, "", ""); err != nil {
		t.Fatalf("existing database required CREATEDB: %v", err)
	}
	if _, err := ConfigureDatabases(ctx, restricted, "", ""); err != nil {
		t.Fatalf("repeat existing database: %v", err)
	}
	_, err = ConfigureDatabases(ctx, restricted, missing, "")
	if err == nil || !strings.Contains(err.Error(), "CREATEDB") || !strings.Contains(err.Error(), "42501") {
		t.Fatalf("missing database should report creation permission: %v", err)
	}
}
