package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSelectDatabase(t *testing.T) {
	for _, tc := range []struct{ name, raw, override, env, want string }{
		{name: "unspecified", want: "looster"},
		{name: "URL inferred role database", raw: "postgres://worker@host?sslmode=disable", want: "worker"},
		{name: "keyword inferred role database", raw: "host=host user=worker", want: "worker"},
		{name: "native", env: "native-db", want: "native-db"},
		{name: "URL path", raw: "postgres://worker:secret@one.example:5544,two.example:5545/custom?sslmode=require&application_name=setup", env: "ignored", want: "custom"},
		{name: "URL query", raw: "postgres://worker@host/path?database=query&sslmode=disable", want: "query"},
		{name: "keyword", raw: "host=host dbname='old db' password='secret' sslmode=require", want: "old db"},
		{name: "URL override", raw: "postgres://worker:secret@host/path?database=query&sslmode=require", override: `new /'\?"`, want: `new /'\?"`},
		{name: "keyword override", raw: "dbname=old host=host sslmode=disable", override: `new '\ dbname=evil`, want: `new '\ dbname=evil`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPGEnvironment(t)
			t.Setenv("PGDATABASE", tc.env)
			t.Setenv("PGPASSWORD", "environment-secret")
			selected, cfg, err := selectDatabase(tc.raw, tc.override, "")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConnConfig.Database != tc.want {
				t.Fatalf("selected %q, want %q", cfg.ConnConfig.Database, tc.want)
			}
			reparsed, err := parsePoolConfig(selected)
			if err != nil {
				t.Fatal(err)
			}
			if reparsed.ConnConfig.Database != tc.want {
				t.Fatalf("round trip database %q", reparsed.ConnConfig.Database)
			}
			original, err := parsePoolConfig(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			a, b := original.ConnConfig, reparsed.ConnConfig
			if a.Host != b.Host || a.Port != b.Port || a.User != b.User || a.Password != b.Password || !reflect.DeepEqual(a.RuntimeParams, b.RuntimeParams) || !reflect.DeepEqual(a.TLSConfig, b.TLSConfig) || !reflect.DeepEqual(a.Fallbacks, b.Fallbacks) {
				t.Fatal("non-database connection settings changed")
			}
			if !strings.Contains(tc.raw, "secret") && strings.Contains(selected, "environment-secret") {
				t.Fatal("serialized environment credential")
			}
		})
	}
}

func TestSelectDatabaseAliasesAndValidation(t *testing.T) {
	clearPGEnvironment(t)
	t.Setenv("PG_DB", "alias-db")
	_, cfg, err := selectDatabase("", "", "")
	if err != nil || cfg.ConnConfig.Database != "alias-db" {
		t.Fatalf("alias not preserved: %v", err)
	}
	for _, names := range [][2]string{{"same", "same"}, {strings.Repeat("a", 64), ""}, {"ok", strings.Repeat("é", 32)}, {"bad\x00name", ""}, {"\xff", ""}} {
		if _, _, err := selectDatabase("", names[0], names[1]); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
}

type provisionRow struct {
	value bool
	err   error
}

func (r provisionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.value
	return nil
}

type provisionFake struct {
	exists            map[string]bool
	queried           []string
	statements        []string
	execErr, queryErr error
	concurrent        bool
	closed            bool
}

func (f *provisionFake) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if sql != "SELECT EXISTS(SELECT FROM pg_database WHERE datname=$1)" {
		panic("unexpected existence query")
	}
	name := args[0].(string)
	f.queried = append(f.queried, name)
	return provisionRow{f.exists[name], f.queryErr}
}
func (f *provisionFake) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.statements = append(f.statements, sql)
	if f.concurrent {
		for _, name := range f.queried {
			f.exists[name] = true
		}
	}
	return pgconn.CommandTag{}, f.execErr
}
func (f *provisionFake) Close(context.Context) error { f.closed = true; return nil }

func TestConfigureDatabases(t *testing.T) {
	for _, catalog := range []string{"", "catalog"} {
		t.Run("catalog="+catalog, func(t *testing.T) {
			clearPGEnvironment(t)
			f := &provisionFake{exists: map[string]bool{}}
			migrated := false
			raw := "postgres://worker:secret@host:5544/custom?sslmode=require"
			selected, err := configureDatabases(context.Background(), raw, "", catalog, func(_ context.Context, cfg *pgx.ConnConfig) (provisioningConn, error) {
				if cfg.Database != "postgres" || cfg.Host != "host" || cfg.Port != 5544 || cfg.Password != "secret" || cfg.TLSConfig == nil {
					t.Fatal("maintenance settings changed")
				}
				return f, nil
			}, func(_ context.Context, raw string) error {
				migrated = true
				cfg, err := parsePoolConfig(raw)
				if err != nil || cfg.ConnConfig.Database != "custom" {
					t.Fatal("wrong migration database")
				}
				return nil
			})
			if err != nil || selected == "" || !migrated || !f.closed {
				t.Fatalf("configuration failed: %v", err)
			}
			want := []string{`CREATE DATABASE "custom"`}
			if catalog != "" {
				want = append(want, `CREATE DATABASE "catalog"`)
			}
			if !reflect.DeepEqual(f.statements, want) {
				t.Fatalf("creation statements: %v", f.statements)
			}
		})
	}
}

func TestEnsureDatabase(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		existing, concurrent bool
		code                 string
		fail                 bool
	}{
		{name: "existing without CREATEDB", existing: true, code: "42501"},
		{name: "create quoted"},
		{name: "duplicate database", concurrent: true, code: "42P04"},
		{name: "concurrent unique index", concurrent: true, code: "23505"},
		{name: "unrelated unique failure", code: "23505", fail: true},
		{name: "permission", code: "42501", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := `strange"; DROP DATABASE other; --`
			f := &provisionFake{exists: map[string]bool{name: tc.existing}, concurrent: tc.concurrent}
			if tc.code != "" {
				f.execErr = &pgconn.PgError{Code: tc.code, Message: "password=secret"}
			}
			err := ensureDatabase(context.Background(), f, name)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leak")
			}
			if tc.existing && len(f.statements) != 0 {
				t.Fatal("existing database required CREATE")
			}
			if !tc.existing && f.statements[0] != `CREATE DATABASE "strange""; DROP DATABASE other; --"` {
				t.Fatal("identifier not quoted")
			}
		})
	}
}

func TestConfigureFailuresAreSanitized(t *testing.T) {
	for _, stage := range []string{"connect", "query", "create", "migrate"} {
		t.Run(stage, func(t *testing.T) {
			clearPGEnvironment(t)
			secret := errors.New("postgres://user:supersecret@host/db")
			f := &provisionFake{exists: map[string]bool{}}
			if stage == "query" {
				f.queryErr = secret
			}
			if stage == "create" {
				f.execErr = secret
			}
			migrated := false
			selected, err := configureDatabases(context.Background(), "dbname=app", "", "", func(context.Context, *pgx.ConnConfig) (provisioningConn, error) {
				if stage == "connect" {
					return nil, secret
				}
				return f, nil
			}, func(context.Context, string) error { migrated = true; return secret })
			if err == nil || strings.Contains(err.Error(), "supersecret") || selected == "" {
				t.Fatalf("unsafe failure: %v", err)
			}
			if migrated != (stage == "migrate") {
				t.Fatal("migrated after provisioning failure")
			}
			if stage != "connect" && !f.closed {
				t.Fatal("connection leaked")
			}
		})
	}
}
