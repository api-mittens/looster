package postgres

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/api-mittens/looster/internal/objectstore"
)

func TestDatabaseConnectionStringNativeConnector(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		env, want map[string]string
	}{
		{name: "native", env: map[string]string{"PGHOST": "native", "PGPORT": "5544", "PGUSER": "worker", "PGPASSWORD": `p'a\ss`, "PGSSLMODE": "require", "PGOPTIONS": "-c statement_timeout=7000"}, want: map[string]string{"host": "native", "port": "5544", "user": "worker", "password": `p'a\ss`, "sslmode": "require", "options": "-c statement_timeout=7000"}},
		{name: "aliases", env: map[string]string{"PG_HOST": "alias", "PG_PORT": "5545", "PG_USER": "worker", "PG_PASSWORD": "alias-secret", "PG_SSL_MODE": "require", "PG_APPNAME": "setup"}, want: map[string]string{"host": "alias", "port": "5545", "user": "worker", "password": "alias-secret", "sslmode": "require", "application_name": "setup"}},
		{name: "URL wins multihost", raw: "postgres://url-user:url-secret@one:5544,two:5545/old?sslmode=require&application_name=setup", env: map[string]string{"PGHOST": "ignored", "PGPASSWORD": "ignored", "PGSSLMODE": "disable"}, want: map[string]string{"host": "one,two", "port": "5544,5545", "user": "url-user", "password": "url-secret", "sslmode": "require", "application_name": "setup"}},
		{name: "keywords quoted data", raw: `host=server user=worker password='p\'ass\\word service=literal' sslmode=disable options='-c statement_timeout=9000'`, want: map[string]string{"host": "server", "user": "worker", "password": `p'ass\word service=literal`, "sslmode": "disable", "options": "-c statement_timeout=9000"}},
		{name: "explicit empty password", raw: "postgres://worker:@server/db?sslmode=disable", env: map[string]string{"PGPASSWORD": "ignored"}, want: map[string]string{"password": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPGEnvironment(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			raw, err := DatabaseConnectionString(tc.raw, `catalog '"\`)
			if err != nil {
				t.Fatal(err)
			}
			values, err := connectorKeywords(raw)
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				if values[key] != want {
					t.Errorf("setting %s changed", key)
				}
			}
			if values["dbname"] != `catalog '"\` {
				t.Fatal("database escaping lost")
			}
			for _, key := range []string{"sslcert", "sslkey", "sslrootcert"} {
				if values[key] != filepath.Join(os.DevNull, "looster-no-"+key) {
					t.Fatalf("implicit TLS discovery not blocked: %s", key)
				}
			}
			if _, ok := values["passfile"]; ok {
				t.Fatal("connector received passfile option")
			}
			if err := ducklake.Validate(ducklake.Config{CatalogConnection: raw, DataPath: "s3://bucket/disposable/", S3: objectstore.Config{AccessKey: "access", SecretKey: "secret"}}); err != nil {
				t.Fatalf("native connector syntax rejected: %v", err)
			}
		})
	}
}

func TestDatabaseConnectionStringTLSPaths(t *testing.T) {
	clearPGEnvironment(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"server"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, keyfile := filepath.Join(t.TempDir(), "client cert.pem"), filepath.Join(t.TempDir(), "client key.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyfile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"PGSSL", "PG_SSL"} {
		t.Run(prefix, func(t *testing.T) {
			t.Setenv(prefix+"CERT", cert)
			t.Setenv(prefix+"KEY", keyfile)
			t.Setenv(prefix+"ROOTCERT", cert)
			t.Setenv("PGSSLMODE", "verify-full")
			raw, err := DatabaseConnectionString("host=server user=worker", "catalog")
			if err != nil {
				t.Fatal(err)
			}
			values, err := connectorKeywords(raw)
			if err != nil {
				t.Fatal(err)
			}
			if values["sslmode"] != "verify-full" || values["sslcert"] != cert || values["sslkey"] != keyfile || values["sslrootcert"] != cert {
				t.Fatal("TLS verification inputs changed")
			}
		})
	}
}

func TestDatabaseConnectionStringRejectsUnsupportedSafely(t *testing.T) {
	for _, raw := range []string{"host=server user=worker password=canary pool_max_conns=2", "host=server user=worker password=canary search_path=public", "postgres://worker:canary@server/db?sslmode=verify-full"} {
		clearPGEnvironment(t)
		if _, err := DatabaseConnectionString(raw, "catalog"); err == nil || strings.Contains(err.Error(), "canary") {
			t.Fatal("unsupported connector settings not safely rejected")
		}
	}
	clearPGEnvironment(t)
	t.Setenv("PGSSLNEGOTIATION", "direct")
	if _, err := DatabaseConnectionString("host=server user=worker", "catalog"); err == nil {
		t.Fatal("unsupported environment setting accepted")
	}
}

func TestConnectorBackslashAndMixedPortSemantics(t *testing.T) {
	clearPGEnvironment(t)
	for _, raw := range []string{
		`host=server user=worker password='a\z' sslmode=disable`,
		`host=server user=worker password=a\z sslmode=disable`,
		`host=server user=worker password='a\\z\'x' sslmode=disable`,
	} {
		cfg, err := parsePoolConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := DatabaseConnectionString(raw, "catalog")
		if err != nil {
			t.Fatal(err)
		}
		values, err := connectorKeywords(resolved)
		if err != nil || values["password"] != cfg.ConnConfig.Password {
			t.Fatal("native libpq escaping changed the currently resolved pgx password")
		}
	}
	for _, raw := range []string{
		"postgres://worker@one,two:5433/db?sslmode=disable",
		"postgres://worker@one:5433,two/db?sslmode=disable",
	} {
		if _, err := DatabaseConnectionString(raw, "catalog"); err == nil {
			t.Fatal("ambiguous mixed URL port alignment accepted")
		}
	}
}
