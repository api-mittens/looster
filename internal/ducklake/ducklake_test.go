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

	"github.com/api-mittens/looster/internal/objectstore"
)

func config() Config {
	return Config{CatalogConnection: "host=localhost dbname=ducklake user=test password='pg-secret' sslmode=verify-full sslrootcert='/tmp/root.crt'", DataPath: "s3://bucket/disposable/", S3: objectstore.Config{AccessKey: "access-canary", SecretKey: "secret-canary", SessionToken: "token-canary", Endpoint: "http://localhost:4566"}}
}

func TestValidateOffline(t *testing.T) {
	c := config()
	c.Binary = "/nonexistent/duckdb"
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	c.Namespace = "Invalid namespace"
	if err := Validate(c); err == nil {
		t.Fatal("expected validation failure")
	}
}

func TestPrepare(t *testing.T) {
	c := config()
	sql, err := prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version() = 'v1.5.2'", "LOAD ducklake", "LOAD postgres", "LOAD httpfs", "CREATE TEMPORARY SECRET", "PROVIDER config", "URL_STYLE 'path'", "ENDPOINT 'localhost:4566'", "USE_SSL false", "REGION 'us-east-1'", "SESSION_TOKEN 'token-canary'", "sslmode=verify-full", "sslrootcert=''/tmp/root.crt''", "passfile=''/dev/null''", "ATTACH 'ducklake:looster_lake'", "DATA_INLINING_ROW_LIMIT 0"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing expected SQL fragment %q", want)
		}
	}
	for _, bad := range []string{"PERSISTENT", "credential_chain", "INSTALL ", "OVERRIDE_DATA_PATH true"} {
		if strings.Contains(sql, bad) {
			t.Errorf("unsafe SQL fragment %q", bad)
		}
	}
}
func TestSQLQuoting(t *testing.T) {
	c := config()
	c.S3.SecretKey = "x'); SELECT error('injection'); --"
	sql, err := prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "SECRET 'x''); SELECT error(''injection''); --'") {
		t.Fatal("secret not escaped as one SQL string")
	}
}
func TestRejectBeforeEngine(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"namespace", func(c *Config) { c.Namespace = "INVALID" }},
		{"url", func(c *Config) { c.CatalogConnection = "postgres://password@localhost/db" }},
		{"missing TLS policy", func(c *Config) { c.CatalogConnection = "host=x dbname=d user=u" }},
		{"service", func(c *Config) { c.CatalogConnection += " service=canary" }},
		{"credential file", func(c *Config) { c.CatalogConnection += " passfile=/tmp/canary" }},
		{"unsupported pgx option", func(c *Config) { c.CatalogConnection += " pool_max_conns=1" }},
		{"malformed", func(c *Config) { c.CatalogConnection += " password='unterminated" }},
		{"not S3", func(c *Config) { c.DataPath = "file:///canary" }},
		{"bucket root", func(c *Config) { c.DataPath = "s3://bucket/" }},
		{"bucket mismatch", func(c *Config) { c.S3.Bucket = "other" }},
		{"partial credentials", func(c *Config) { c.S3.SecretKey = "" }},
		{"endpoint userinfo", func(c *Config) { c.S3.Endpoint = "https://canary:secret@host" }},
		{"endpoint path", func(c *Config) { c.S3.Endpoint = "https://host/path" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := config()
			tc.mutate(&c)
			called := false
			_, err := open(context.Background(), c, func(context.Context, string, string) error { called = true; return nil })
			if err == nil || called {
				t.Fatal("invalid config reached engine")
			}
			if strings.Contains(err.Error(), "canary") {
				t.Fatal("error leaked value")
			}
		})
	}
}
func TestConnectionPreserved(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		c := config()
		c.CatalogConnection = "host='host1,host2' port='5432,5433' dbname='my db' user='a\\'b' password='a\\\\b' sslmode=" + mode + " sslcert='/path/cert' sslkey='/path/key' sslpassword='key secret' application_name='looster' connect_timeout=4"
		sql, err := prepare(c)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sql, quote("postgres:"+c.CatalogConnection+" passfile='/dev/null'")) {
			t.Fatal("libpq semantics changed")
		}
	}
	for _, raw := range []string{"host=x dbname=d user=u sslmode=require password='x'junk", "host=x dbname=d user=u sslmode=require password=x\\", "host=x dbname=d user=u sslmode=require password='x\\'"} {
		if validConnection(raw) {
			t.Error("accepted malformed libpq settings")
		}
	}
}
func TestCredentialPolicy(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "env-token")
	c := config()
	c.S3.AccessKey = ""
	c.S3.SecretKey = ""
	c.S3.SessionToken = ""
	sql, err := prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"env-access", "env-secret", "env-token"} {
		if !strings.Contains(sql, v) {
			t.Fatal("missing environment credential")
		}
	}
	c.S3.SessionToken = "explicit-token"
	if _, err = prepare(c); err == nil {
		t.Fatal("partial explicit credentials must not fall back")
	}
	c = config()
	sql, err = prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "env-access") {
		t.Fatal("explicit config must win")
	}
}
func TestExecReattachAndSanitize(t *testing.T) {
	calls := []string{}
	l, err := open(context.Background(), config(), func(_ context.Context, _ string, sql string) error { calls = append(calls, sql); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Exec(context.Background(), "SELECT 42;"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.HasSuffix(calls[1], "SELECT 42;") || !strings.Contains(calls[1], "ATTACH") {
		t.Fatal("exec did not reattach")
	}
	l.run = func(context.Context, string, string) error {
		return errors.New("password=pg-secret access-canary token-canary")
	}
	err = l.Exec(context.Background(), "")
	if err == nil {
		t.Fatal("expected engine failure")
	}
	for _, secret := range []string{"pg-secret", "access-canary", "token-canary"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("error leaked secret")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(l.Exec(ctx, ""), context.Canceled) {
		t.Fatal("cancellation lost")
	}
}
func TestProbeSequence(t *testing.T) {
	if ProbeDisposable(context.Background(), Config{}, false) == nil {
		t.Fatal("missing destructive probe guard")
	}
	var calls []string
	l, err := open(context.Background(), config(), func(_ context.Context, _ string, s string) error { calls = append(calls, s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = l.probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("want attach/write/reopen/cleanup, got %d", len(calls))
	}
	for i, want := range []string{"ATTACH", "INSERT INTO lake.main.looster_probe_", "array_agg(id)=[42]", "ducklake_cleanup_old_files('lake', cleanup_all => true)"} {
		if !strings.Contains(calls[i], want) {
			t.Errorf("missing probe phase %d", i)
		}
	}
}
func TestCLIBoundary(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-secret")
	t.Setenv("PGPASSWORD", "ambient-pg-secret")
	path := filepath.Join(t.TempDir(), "engine")
	script := "#!/bin/sh\n[ \"$#\" = 5 ] || exit 1\n[ \"$1\" = -init ] && [ \"$2\" = /dev/null ] && [ \"$3\" = -batch ] && [ \"$4\" = -bail ] && [ \"$5\" = :memory: ] || exit 2\n[ -z \"$AWS_ACCESS_KEY_ID$PGPASSWORD\" ] || exit 3\nIFS= read -r line\n[ \"$line\" = stdin-secret-canary ] || exit 4\necho secret-diagnostic >&2\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(context.Background(), path, "stdin-secret-canary\n"); err != nil {
		t.Fatal("CLI argv/environment/stdin boundary failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if runCLI(ctx, path, "") == nil {
		t.Fatal("expected canceled CLI")
	}
}

func TestFormattingRedacted(t *testing.T) {
	c := config()
	l, err := open(context.Background(), c, func(context.Context, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{c, &c, l} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			rendered := fmt.Sprintf(format, value)
			for _, secret := range []string{"pg-secret", "access-canary", "token-canary"} {
				if strings.Contains(rendered, secret) {
					t.Fatal("formatted value leaked credentials")
				}
			}
		}
	}
}

// This is intentionally opt-in and cannot use normal application config. The
// operator must provide a fresh, exclusively owned catalog and prefix. It writes
// and deletes objects and expires all history in that disposable catalog.
func TestDisposableIntegration(t *testing.T) {
	if os.Getenv("LOOSTER_DUCKLAKE_DISPOSABLE_PROBE") != "yes" {
		t.Skip("requires explicitly disposable PostgreSQL catalog, S3 prefix and pinned DuckDB CLI/extensions")
	}
	for _, key := range []string{"LOOSTER_TEST_DUCKDB", "LOOSTER_TEST_DUCKLAKE_LIBPQ", "LOOSTER_TEST_DUCKLAKE_DATA_PATH", "LOOSTER_TEST_S3_ACCESS_KEY", "LOOSTER_TEST_S3_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("disposable integration requires explicit %s", key)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := Config{Binary: os.Getenv("LOOSTER_TEST_DUCKDB"), CatalogConnection: os.Getenv("LOOSTER_TEST_DUCKLAKE_LIBPQ"), DataPath: os.Getenv("LOOSTER_TEST_DUCKLAKE_DATA_PATH"), S3: objectstore.Config{Endpoint: os.Getenv("LOOSTER_TEST_S3_ENDPOINT"), Region: os.Getenv("LOOSTER_TEST_S3_REGION"), AccessKey: os.Getenv("LOOSTER_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("LOOSTER_TEST_S3_SECRET_KEY"), SessionToken: os.Getenv("LOOSTER_TEST_S3_SESSION_TOKEN")}}
	if err := ProbeDisposable(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	l, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Exec(ctx, "SELECT CASE WHEN count(*)=0 THEN true ELSE error('probe table remains') END FROM duckdb_tables() WHERE database_name='lake' AND table_name LIKE 'looster_probe_%';"); err != nil {
		t.Fatal(err)
	}
	conflicting := c
	conflicting.DataPath = c.DataPath + "conflicting/"
	if _, err := Open(ctx, conflicting); err == nil {
		t.Fatal("existing catalog accepted a conflicting S3 data path")
	}
	// ATTACH alone must still succeed with an unreachable endpoint. A real
	// write, unlike metadata initialization, must fail with sanitized diagnostics.
	bad := c
	bad.S3.Endpoint = "http://127.0.0.1:1"
	l, err = Open(ctx, bad)
	if err != nil {
		t.Fatal("metadata-only attach unexpectedly required S3")
	}
	err = l.Exec(ctx, "COPY (SELECT 42 AS id) TO "+quote(c.DataPath+"bad-endpoint-probe.parquet")+" (FORMAT parquet);")
	if err == nil {
		t.Fatal("unreachable S3 endpoint accepted write")
	}
	for _, secret := range []string{c.CatalogConnection, c.S3.AccessKey, c.S3.SecretKey, c.S3.SessionToken, bad.S3.Endpoint} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatal("S3 failure leaked configured values")
		}
	}
	missing := c
	missing.Binary = filepath.Join(t.TempDir(), "missing-duckdb")
	if _, err := Open(ctx, missing); err == nil {
		t.Fatal("missing engine accepted")
	}
	t.Setenv("HOME", t.TempDir())
	if _, err := Open(ctx, c); err != nil {
		t.Fatal("automatic extension installation failed")
	}
}
