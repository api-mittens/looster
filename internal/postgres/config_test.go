package postgres

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPoolLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, raw    string
		idle, health time.Duration
		bad          bool
	}{
		{name: "environment only", idle: 30 * time.Second, health: 10 * time.Second},
		{name: "URL defaults", raw: "postgres://worker@localhost/test?sslmode=disable", idle: 30 * time.Second, health: 10 * time.Second},
		{name: "keyword defaults", raw: "host=localhost user=worker", idle: 30 * time.Second, health: 10 * time.Second},
		{name: "URL above defaults", raw: "postgres://worker@localhost/test?pool_max_conn_idle_time=30m&pool_health_check_period=1m&pool_max_conns=60", idle: 30 * time.Minute, health: time.Minute},
		{name: "keyword above defaults", raw: "host=localhost pool_max_conn_idle_time='30m' pool_health_check_period=1m pool_max_conns=60", idle: 30 * time.Minute, health: time.Minute},
		{name: "URL below defaults", raw: "postgres://worker@localhost/test?pool_max_conn_idle_time=5s&pool_health_check_period=2s", idle: 5 * time.Second, health: 2 * time.Second},
		{name: "keyword below defaults", raw: "host=localhost pool_max_conn_idle_time=5s pool_health_check_period=2s", idle: 5 * time.Second, health: 2 * time.Second},
		{name: "URL idle only", raw: "postgresql://worker@localhost/test?pool_max_conn_idle_time=2h", idle: 2 * time.Hour, health: 10 * time.Second},
		{name: "keyword health only", raw: "host=localhost pool_health_check_period=2m", idle: 30 * time.Second, health: 2 * time.Minute},
		{name: "parameter-looking password", raw: "host=localhost password='pool_max_conn_idle_time=2h pool_health_check_period=2m'", idle: 30 * time.Second, health: 10 * time.Second},
		{name: "explicit zero idle", raw: "host=localhost pool_max_conn_idle_time=0", idle: 0, health: 10 * time.Second},
		{name: "invalid URL idle", raw: "postgres://worker@localhost/test?pool_max_conn_idle_time=invalid", bad: true},
		{name: "empty URL health", raw: "postgres://worker@localhost/test?pool_health_check_period=", bad: true},
		{name: "invalid keyword health", raw: "host=localhost pool_health_check_period=invalid", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPGEnvironment(t)
			t.Setenv("PGUSER", "worker")
			t.Setenv("PGSSLMODE", "disable")
			// LOOSTER_DATABASE_URL supplies the same raw pool parameters as a
			// directly provided connection string. No database is contacted.
			t.Setenv("LOOSTER_DATABASE_URL", tc.raw)
			cfg, err := parsePoolConfig(os.Getenv("LOOSTER_DATABASE_URL"))
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected config error: %v", err)
			}
			if tc.bad {
				return
			}
			if cfg.MaxConnIdleTime != tc.idle || cfg.HealthCheckPeriod != tc.health {
				t.Fatalf("idle=%s health=%s, want idle=%s health=%s", cfg.MaxConnIdleTime, cfg.HealthCheckPeriod, tc.idle, tc.health)
			}
			baseline, err := pgxpool.ParseConfig(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MaxConns != baseline.MaxConns || cfg.MinConns != baseline.MinConns || cfg.MinIdleConns != baseline.MinIdleConns || cfg.MaxConnLifetime != baseline.MaxConnLifetime || cfg.MaxConnLifetimeJitter != baseline.MaxConnLifetimeJitter {
				t.Fatal("unrelated pool settings changed")
			}
			for _, key := range []string{"pool_max_conn_idle_time", "pool_health_check_period"} {
				if _, ok := cfg.ConnConfig.RuntimeParams[key]; ok {
					t.Fatalf("pool option %s leaked into server parameters", key)
				}
			}
		})
	}
}

func TestPostgresEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, raw                      string
		env                            map[string]string
		host, user, password, database string
		port                           uint16
		tls                            bool
		bad                            bool
	}{
		{name: "native variables without URL", host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work"},
		{name: "underscore aliases", env: map[string]string{"PGHOST": "", "PGPORT": "", "PGUSER": "", "PGPASSWORD": "", "PGDATABASE": "", "PG_HOST": "alias.example", "PG_PORT": "5545", "PG_USER": "alias-user", "PG_PASS": "alias-secret", "PG_DB": "alias-db"}, host: "alias.example", port: 5545, user: "alias-user", password: "alias-secret", database: "alias-db"},
		{name: "alternative aliases", env: map[string]string{"PGPASSWORD": "", "PGDATABASE": "", "PG_PASSWORD": "alias-secret", "PG_DATABASE": "alias-db"}, host: "pg.example", port: 5544, user: "worker", password: "alias-secret", database: "alias-db"},
		{name: "native wins over alias", env: map[string]string{"PG_HOST": "ignored", "PG_PASS": "ignored", "PG_PASSWORD": "ignored"}, host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work"},
		{name: "URL wins over environment", raw: "postgres://url-user:url-secret@url.example:5546/url-db?sslmode=disable", env: map[string]string{"PG_PASS": "ignored"}, host: "url.example", port: 5546, user: "url-user", password: "url-secret", database: "url-db"},
		{name: "partial URL native fallback", raw: "postgres://url-user@url.example/url-db", host: "url.example", port: 5544, user: "url-user", password: "native-secret", database: "url-db"},
		{name: "partial URL alias fallback", raw: "postgres://url-user@url.example/url-db", env: map[string]string{"PGPASSWORD": "", "PG_PASS": "alias-secret", "PG_HOST": "ignored"}, host: "url.example", port: 5544, user: "url-user", password: "alias-secret", database: "url-db"},
		{name: "explicit empty URL password", raw: "postgres://url-user:@url.example/url-db", env: map[string]string{"PGPASSWORD": "", "PG_PASS": "ignored"}, host: "url.example", port: 5544, user: "url-user", database: "url-db"},
		{name: "URL query overrides aliases", raw: "postgres:///?host=query.example&port=5547&user=query-user&database=query-db&password=query-secret", env: map[string]string{"PGPASSWORD": "", "PG_PASS": "ignored", "PGDATABASE": "", "PG_DB": "ignored"}, host: "query.example", port: 5547, user: "query-user", password: "query-secret", database: "query-db"},
		{name: "keyword string overrides aliases", raw: "host=keyword.example user=keyword-user password=keyword-secret dbname=keyword-db port=5548", env: map[string]string{"PGPASSWORD": "", "PG_PASS": "ignored", "PG_HOST": "ignored"}, host: "keyword.example", port: 5548, user: "keyword-user", password: "keyword-secret", database: "keyword-db"},
		{name: "alias password escaping", env: map[string]string{"PGPASSWORD": "", "PG_PASS": `p'a\ss host=evil.example`}, host: "pg.example", port: 5544, user: "worker", password: `p'a\ss host=evil.example`, database: "work"},
		{name: "URL alias password escaping", raw: "postgres://url-user@url.example/url-db", env: map[string]string{"PGPASSWORD": "", "PG_PASS": `p'a\ss &?/host=evil.example`}, host: "url.example", port: 5544, user: "url-user", password: `p'a\ss &?/host=evil.example`, database: "url-db"},
		{name: "TLS alias", env: map[string]string{"PGSSLMODE": "", "PG_SSL_MODE": "require"}, host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work", tls: true},
		{name: "native TLS", env: map[string]string{"PGSSLMODE": "require"}, host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work", tls: true},
		{name: "timeout and application aliases", env: map[string]string{"PGCONNECT_TIMEOUT": "", "PG_CONNECT_TIMEOUT": "7", "PGAPPNAME": "", "PG_APPNAME": "looster-test"}, host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work"},
		{name: "URL TLS wins over alias", raw: "postgres:///?sslmode=disable", env: map[string]string{"PGSSLMODE": "", "PG_SSLMODE": "require"}, host: "pg.example", port: 5544, user: "worker", password: "native-secret", database: "work"},
		{name: "multiple hosts preserved", raw: "postgres://url-user:url-secret@one.example:5433,two.example:5434/url-db", env: map[string]string{"PGHOST": "", "PG_HOST": "ignored", "PGPORT": "", "PG_PORT": "5555"}, host: "one.example", port: 5433, user: "url-user", password: "url-secret", database: "url-db"},
		{name: "invalid alias port", env: map[string]string{"PGPORT": "", "PG_PORT": "invalid"}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPGEnvironment(t)
			for k, v := range map[string]string{"PGHOST": "pg.example", "PGPORT": "5544", "PGUSER": "worker", "PGPASSWORD": "native-secret", "PGDATABASE": "work", "PGSSLMODE": "disable", "PGAPPNAME": "looster-test", "PGCONNECT_TIMEOUT": "7"} {
				t.Setenv(k, v)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := parsePoolConfig(tc.raw)
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected config error: %v", err)
			}
			if tc.bad {
				return
			}
			c := cfg.ConnConfig
			if c.Host != tc.host || c.Port != tc.port || c.User != tc.user || c.Database != tc.database {
				t.Fatalf("incorrect connection target: host=%s port=%d user=%s database=%s", c.Host, c.Port, c.User, c.Database)
			}
			if c.Password != tc.password {
				t.Fatal("incorrect password resolution")
			}
			if (c.TLSConfig != nil) != tc.tls {
				t.Fatal("incorrect TLS configuration")
			}
			if c.ConnectTimeout != 7*time.Second || c.RuntimeParams["application_name"] != "looster-test" {
				t.Fatal("native timeout/application settings ignored")
			}
		})
	}
}

func TestPostgresPasswordFile(t *testing.T) {
	clearPGEnvironment(t)
	file := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(file, []byte("pg.example:5544:work:worker:file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"PGHOST": "pg.example", "PGPORT": "5544", "PGUSER": "worker", "PGDATABASE": "work", "PGPASSFILE": file, "PGSSLMODE": "disable"} {
		t.Setenv(k, v)
	}
	_, err := parsePoolConfig("")
	if err == nil || !strings.Contains(err.Error(), "PGPASSFILE") || strings.Contains(err.Error(), file) || strings.Contains(err.Error(), "file-secret") {
		t.Fatal("password file setting must be rejected without its value")
	}
}

func clearPGEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		// Avoid ambient service files, credentials or TLS settings in parser tests.
		if len(entry) >= 2 && entry[:2] == "PG" {
			for i := range entry {
				if entry[i] == '=' {
					t.Setenv(entry[:i], "")
					break
				}
			}
		}
	}
	for _, entry := range pgAliases {
		t.Setenv(entry.standard, "")
		for _, alias := range entry.aliases {
			t.Setenv(alias, "")
		}
	}
}

func TestPostgresRejectsCredentialFiles(t *testing.T) {
	for _, key := range []string{"service", "servicefile", "passfile"} {
		for _, raw := range []string{key + "='do-not-print'", "password='quoted service=inside password' " + key + " = 'do-not-print'", "postgres:///?" + key + "=do-not-print"} {
			t.Run(key+raw[:2], func(t *testing.T) {
				clearPGEnvironment(t)
				_, err := parsePoolConfig(raw)
				if err == nil || !strings.Contains(err.Error(), "connection key "+key+" is not supported") || strings.Contains(err.Error(), "do-not-print") {
					t.Fatal("credential file key not safely rejected")
				}
			})
		}
	}
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE", "PGPASSFILE", "PG_PASSFILE"} {
		t.Run(key, func(t *testing.T) {
			clearPGEnvironment(t)
			t.Setenv(key, "do-not-print")
			_, err := parsePoolConfig("")
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "do-not-print") {
				t.Fatal("file environment key not safely rejected")
			}
		})
	}
}

func TestPostgresImplicitFilesDisabledAndTLSPathsPreserved(t *testing.T) {
	clearPGEnvironment(t)
	for _, raw := range []string{"", "postgres:///?sslmode=disable"} {
		conn, err := connectionString(raw, func(string) string { return "" })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(conn, "passfile=") || !strings.Contains(conn, os.DevNull) && !strings.Contains(conn, url.QueryEscape(os.DevNull)) {
			t.Fatal("implicit password file not disabled")
		}
	}
	for _, prefix := range []string{"PGSSL", "PG_SSL"} {
		t.Run(prefix, func(t *testing.T) {
			env := map[string]string{prefix + "CERT": "client-cert.pem", prefix + "KEY": "client-key.pem", prefix + "ROOTCERT": "root-cert.pem"}
			conn, err := connectionString("postgres:///?sslmode=require", func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(conn)
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"sslcert": "client-cert.pem", "sslkey": "client-key.pem", "sslrootcert": "root-cert.pem"} {
				if u.Query().Get(key) != want {
					t.Fatalf("TLS env path lost: %s", key)
				}
			}
		})
	}
	// Literal parameter-looking text in a quoted password must remain data.
	t.Setenv("PGSSLMODE", "disable")
	cfg, err := parsePoolConfig(`host=localhost user=worker dbname=test password='service=literal passfile=literal'`)
	if err != nil || cfg.ConnConfig.Password != "service=literal passfile=literal" {
		t.Fatal("quoted password treated as settings")
	}
}

func TestPostgresParseErrorsDoNotLeakSecrets(t *testing.T) {
	clearPGEnvironment(t)
	for _, raw := range []string{"postgres://user:do-not-print@localhost:invalid/db", "password='do-not-print' port=invalid"} {
		_, err := parsePoolConfig(raw)
		if err == nil || strings.Contains(err.Error(), "do-not-print") {
			t.Fatal("connection parse error leaked secret")
		}
	}
}

func TestPostgresEnvironmentTLSFiles(t *testing.T) {
	clearPGEnvironment(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "client cert.pem"), filepath.Join(dir, "client key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"PGSSL", "PG_SSL"} {
		t.Run(prefix, func(t *testing.T) {
			t.Setenv(prefix+"CERT", certFile)
			t.Setenv(prefix+"KEY", keyFile)
			t.Setenv(prefix+"ROOTCERT", certFile)
			t.Setenv("PGSSLMODE", "require")
			cfg, err := parsePoolConfig("host=localhost user=test dbname=test")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConnConfig.TLSConfig == nil || len(cfg.ConnConfig.TLSConfig.Certificates) != 1 || cfg.ConnConfig.TLSConfig.RootCAs == nil {
				t.Fatal("environment TLS files ignored")
			}
		})
	}
}
