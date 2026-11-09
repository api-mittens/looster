package ducklake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/objectstore"
)

func config() Config {
	return Config{CatalogConnection: "host=localhost port=5432 dbname=ducklake user=test password='pg-secret' sslmode=verify-full sslrootcert='/tmp/root.crt'", DataPath: "s3://bucket/disposable/", S3: objectstore.Config{AccessKey: "access-canary", SecretKey: "secret-canary", SessionToken: "token-canary", Endpoint: "http://localhost:4566"}}
}

func TestValidateOffline(t *testing.T) {
	c := config()
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Namespace = "other" },
		func(c *Config) { c.CatalogConnection = "postgres://secret@host/db" },
		func(c *Config) { c.CatalogConnection = "host=x dbname=d user=u" },
		func(c *Config) { c.CatalogConnection += " service=secret" },
		func(c *Config) { c.CatalogConnection += " passfile=/tmp/secret" },
		func(c *Config) { c.CatalogConnection += " pool_max_conns=1" },
		func(c *Config) { c.CatalogConnection += " password='unterminated" },
		func(c *Config) { c.DataPath = "file:///secret" },
		func(c *Config) { c.DataPath = "s3://bucket/" },
		func(c *Config) { c.S3.Bucket = "other" },
		func(c *Config) { c.S3.SecretKey = "" },
		func(c *Config) { c.S3.Endpoint = "https://secret:password@host" },
		func(c *Config) { c.S3.Endpoint = "https://host/path" },
	} {
		c := config()
		mutate(&c)
		if _, err := Open(context.Background(), c); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestPrepare(t *testing.T) {
	p, err := prepare(config())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version() = 'v1.5.2'", "allow_unsigned_extensions=false", "LOAD httpfs", "INSTALL postgres FROM 'https://extensions.duckdb.org'", "INSTALL ducklake FROM 'https://extensions.duckdb.org'", "LOAD ducklake", "CREATE TEMPORARY SECRET", "PROVIDER config", "URL_STYLE 'path'", "USE_SSL false", "REGION 'us-east-1'", "SESSION_TOKEN 'token-canary'", "passfile=''" + os.DevNull + "''", "DATA_INLINING_ROW_LIMIT 0", DuckLakeExtensionVersion, PostgresExtensionVersion, HTTPFSExtensionVersion} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"PERSISTENT", "credential_chain", "OVERRIDE_DATA_PATH true"} {
		if strings.Contains(p, bad) {
			t.Errorf("unsafe %s", bad)
		}
	}
	c := config()
	c.S3.SecretKey = "x'); SELECT error('injection'); --"
	p, err = prepare(c)
	if err != nil || !strings.Contains(p, "SECRET 'x''); SELECT error(''injection''); --'") {
		t.Fatal("SQL quote failure")
	}
}

func TestConnectionPreserved(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		c := config()
		c.CatalogConnection = "host='host1,host2' port='5432,5433' dbname='my db' user='a\\'b' password='a\\\\b' sslmode=" + mode + " sslcert='/path/cert' sslkey='/path/key' sslpassword='key secret' application_name=looster connect_timeout=4"
		p, err := prepare(c)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p, quote("postgres:"+c.CatalogConnection+" passfile='"+os.DevNull+"'")) {
			t.Fatal("libpq settings changed")
		}
	}
	for _, raw := range []string{"host=x dbname=d user=u sslmode=require password='x'junk", "host=x dbname=d user=u sslmode=require password=x\\", "host=x dbname=d user=u sslmode=require password='x\\'"} {
		if validConnection(raw) {
			t.Fatal("malformed connection accepted")
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
	p, err := prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"env-access", "env-secret", "env-token"} {
		if !strings.Contains(p, v) {
			t.Fatal("missing credential")
		}
	}
	c.S3.SessionToken = "explicit"
	if Validate(c) == nil {
		t.Fatal("partial explicit credentials fell back")
	}
	p, _ = prepare(config())
	if strings.Contains(p, "env-access") {
		t.Fatal("environment overrides config")
	}
}

func engine(t *testing.T) *Lake {
	t.Helper()
	l, err := openEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return l
}

func TestEmbeddedLifetime(t *testing.T) {
	l := engine(t)
	ctx := context.Background()
	for _, q := range []string{"SELECT CASE WHEN version()='" + EngineVersion + "' THEN true ELSE error('version') END", "CREATE TEMP TABLE retained(i INTEGER)", "BEGIN", "INSERT INTO retained VALUES (42)", "COMMIT", "SELECT CASE WHEN array_agg(i)=[42] THEN true ELSE error('lost session') END FROM retained"} {
		if err := l.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(l.Exec(ctx, "SELECT 1"), ErrClosed) {
		t.Fatal("use after close accepted")
	}
	fresh := engine(t)
	if fresh.Exec(ctx, "SELECT * FROM retained") == nil {
		t.Fatal("fresh engine retained temp table")
	}
}

func TestCancellationAndRedaction(t *testing.T) {
	l := engine(t)
	if err := l.Exec(context.Background(), "SELECT error('password=pg-secret access-canary token-canary')"); err == nil || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "pg-secret") {
		t.Fatal("engine error not redacted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(l.Exec(ctx, "SELECT 1"), context.Canceled) {
		t.Fatal("pre-cancellation lost")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Exec(ctx, "SELECT sum(i*j) FROM range(1000000000) a(i), range(1000000000) b(j)"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
	if err := l.Exec(context.Background(), "SELECT 42"); err != nil {
		t.Fatal("session unusable after cancellation", err)
	}
	for _, value := range []any{config(), l} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			s := fmt.Sprintf(format, value)
			if strings.Contains(s, "canary") || strings.Contains(s, "pg-secret") {
				t.Fatal("format leaked")
			}
		}
	}
}

func TestCloseCancelsActiveWork(t *testing.T) {
	l := engine(t)
	done := make(chan error, 1)
	go func() {
		done <- l.Exec(context.Background(), "SELECT sum(i*j) FROM range(1000000000) a(i), range(1000000000) b(j)")
	}()
	time.Sleep(30 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("unexpected active result %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("query did not stop")
	}
}

// This test exercises real official signed artifacts, without PostgreSQL/S3.
func TestOfficialExtensions(t *testing.T) {
	if os.Getenv("LOOSTER_TEST_DUCKLAKE_EXTENSIONS") != "yes" {
		t.Skip("opt-in HTTPS official extension installation")
	}
	l := engine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	directory := t.TempDir()
	if err := l.Exec(ctx, "SET extension_directory="+quote(directory)); err != nil {
		t.Fatal(err)
	}
	p, err := prepare(config())
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := strings.SplitN(p, "CREATE TEMPORARY SECRET", 2)[0]
	// Use the actual production bootstrap on an empty extension directory.
	if err := l.bootstrapHTTPFS(ctx, directory); err != nil {
		t.Fatal(err)
	}
	if _, err := l.conn.ExecContext(ctx, bootstrap); err != nil {
		t.Fatal("fresh official extension bootstrap", err)
	}
	rows, err := l.conn.QueryContext(ctx, "SELECT extension_name, extension_version FROM duckdb_extensions() WHERE loaded")
	if err != nil {
		t.Fatal("extension inventory failed")
	}
	defer rows.Close()
	versions := map[string]string{}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			t.Fatal(err)
		}
		versions[n] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for n, v := range map[string]string{"ducklake": DuckLakeExtensionVersion, "postgres_scanner": PostgresExtensionVersion, "httpfs": HTTPFSExtensionVersion} {
		if versions[n] != v {
			t.Errorf("%s version: got %q want %q", n, versions[n], v)
		}
	}
	rows.Close()
	incompatible := strings.Replace(bootstrap, DuckLakeExtensionVersion, "incompatible-canary", 1)
	if err := l.Exec(ctx, incompatible); err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("incompatible loaded extension did not fail safely: %v", err)
	}
	p, _ = prepare(config())
	prefix := strings.Split(p, "ATTACH 'ducklake:")[0]
	if err := l.Exec(ctx, prefix); err != nil {
		t.Fatal("production bootstrap failed", err)
	}
	if err := l.Exec(ctx, "SELECT CASE WHEN count(*)=2 AND bool_and(NOT persistent AND storage='memory') THEN true ELSE error('secret persistence') END FROM duckdb_secrets() WHERE name IN ('looster_s3','looster_lake')"); err != nil {
		t.Fatal("temporary secret policy failed", err)
	}
}

func TestFreshOpenBootstrap(t *testing.T) {
	if os.Getenv("LOOSTER_TEST_DUCKLAKE_EXTENSIONS") != "yes" {
		t.Skip("opt-in fresh HTTPS Open bootstrap")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	c := config()
	c.CatalogConnection = "host=127.0.0.1 port=1 dbname=unreachable user=test password='resolved-password' sslmode=disable connect_timeout=1"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for i := 0; i < 2; i++ {
		lake, err := Open(ctx, c)
		if lake != nil {
			lake.Close()
			t.Fatal("unreachable catalog unexpectedly opened")
		}
		if err == nil || !strings.Contains(err.Error(), "temporary secrets/catalog attachment") {
			t.Fatalf("fresh/cached Open did not pass extension bootstrap: %v", err)
		}
	}
}

func TestDisposableIntegration(t *testing.T) {
	if ProbeDisposable(context.Background(), Config{}, false) == nil {
		t.Fatal("missing destructive guard")
	}
	if os.Getenv("LOOSTER_DUCKLAKE_DISPOSABLE_PROBE") != "yes" {
		t.Skip("requires disposable PostgreSQL catalog and S3 prefix")
	}
	for _, key := range []string{"LOOSTER_TEST_DUCKLAKE_LIBPQ", "LOOSTER_TEST_DUCKLAKE_DATA_PATH", "LOOSTER_TEST_S3_ACCESS_KEY", "LOOSTER_TEST_S3_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("requires %s", key)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := Config{CatalogConnection: os.Getenv("LOOSTER_TEST_DUCKLAKE_LIBPQ"), DataPath: os.Getenv("LOOSTER_TEST_DUCKLAKE_DATA_PATH"), S3: objectstore.Config{Endpoint: os.Getenv("LOOSTER_TEST_S3_ENDPOINT"), Region: os.Getenv("LOOSTER_TEST_S3_REGION"), AccessKey: os.Getenv("LOOSTER_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("LOOSTER_TEST_S3_SECRET_KEY"), SessionToken: os.Getenv("LOOSTER_TEST_S3_SESSION_TOKEN")}}
	if err := ProbeDisposable(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	l, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Exec(ctx, "SELECT CASE WHEN count(*)=0 THEN true ELSE error('probe remains') END FROM duckdb_tables() WHERE database_name='lake' AND table_name LIKE 'looster_probe_%'"); err != nil {
		t.Fatal(err)
	}
	conflicting := c
	conflicting.DataPath += "conflicting/"
	if other, err := Open(ctx, conflicting); err == nil {
		other.Close()
		t.Fatal("conflicting data path accepted")
	}
	bad := c
	bad.S3.Endpoint = "http://127.0.0.1:1"
	offline, err := Open(ctx, bad)
	if err != nil {
		t.Fatal("metadata-only attach unexpectedly required S3", err)
	}
	defer offline.Close()
	err = offline.Exec(ctx, "COPY (SELECT 42 AS id) TO "+quote(c.DataPath+"bad-endpoint-probe.parquet")+" (FORMAT parquet)")
	if err == nil {
		t.Fatal("unreachable S3 endpoint accepted write")
	}
	for _, secret := range []string{c.CatalogConnection, c.S3.AccessKey, c.S3.SecretKey, c.S3.SessionToken, bad.S3.Endpoint} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatal("S3 failure leaked configured values")
		}
	}
}
