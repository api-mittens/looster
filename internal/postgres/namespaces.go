package postgres

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/postgres/db"
)

const namespaceSchemaPrefix = "looster_ns_"

func namespaceSchema(name string) (string, error) {
	if err := namespace.Validate(name); err != nil {
		return "", err
	}
	if name == namespace.Default {
		return "public", nil
	}
	return namespaceSchemaPrefix + name, nil
}

func namespaceConfig(url, name string) (*pgxpool.Config, error) {
	return namespaceConfigWithLockTimeout(url, name, "5s")
}

func namespaceConfigWithLockTimeout(url, name, fallback string) (*pgxpool.Config, error) {
	schema, err := namespaceSchema(name)
	if err != nil {
		return nil, err
	}
	cfg, err := parsePoolConfig(url)
	if err != nil {
		return nil, err
	}
	// Never fall back to public: absent objects must fail rather than leak data.
	cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize() + ",pg_temp"
	// Apply the fallback after startup so role/database settings and PGOPTIONS
	// remain authoritative. A nonzero lock_timeout (including an explicit DSN
	// value) is preserved. Zero means unbounded, so receives our bounded default.
	// The low-level hook also runs for pgx.ConnectConfig and stdlib.OpenDB:
	// migration serialization and Goose DDL must not wait indefinitely either.
	previous := cfg.ConnConfig.Config.AfterConnect
	cfg.ConnConfig.Config.AfterConnect = func(ctx context.Context, conn *pgconn.PgConn) error {
		if previous != nil {
			if err := previous(ctx, conn); err != nil {
				return err
			}
		}
		result := conn.ExecParams(ctx, `SELECT set_config('lock_timeout',$1,false)
 WHERE current_setting('lock_timeout') = '0'`, [][]byte{[]byte(fallback)}, nil, nil, nil).Read()
		return result.Err
	}
	return cfg, nil
}

// OpenNamespace opens an existing namespace. Creating namespaces is explicit.
func OpenNamespace(ctx context.Context, url, name string) (*Store, error) {
	cfg, err := namespaceConfig(url, name)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	schema, _ := namespaceSchema(name)
	var exists bool
	err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname=$1 AND has_schema_privilege(oid,'USAGE'))`, schema).Scan(&exists)
	if err == nil && !exists {
		err = fmt.Errorf("namespace %q does not exist or is inaccessible (use namespace create)", name)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool, q: db.New(pool)}, nil
}

// EnsureNamespace creates the schema if needed and applies all migrations.
// Existing public data remains the default namespace, with its migration history.
func EnsureNamespace(ctx context.Context, url, name string) error {
	return migrateNamespace(ctx, url, name, true)
}

// MigrateNamespace applies the embedded migration history independently per schema.
func MigrateNamespace(ctx context.Context, url, name string) error {
	return migrateNamespace(ctx, url, name, false)
}

func migrateNamespace(ctx context.Context, url, name string, ensure bool) error {
	// A concurrent initializer may be building several indexes before releasing
	// the namespace lock. Give migrations a longer, still finite fallback than
	// interactive mutations, while preserving explicit deployment settings.
	cfg, err := namespaceConfigWithLockTimeout(url, name, "30s")
	if err != nil {
		return err
	}
	schema, _ := namespaceSchema(name)
	// Serialize both schema creation and Goose history updates for this namespace.
	// The dedicated connection owns the lock until all migration work is finished.
	lock, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return err
	}
	defer lock.Close(context.Background())
	if err = acquireMigrationLock(ctx, lock, schema); err != nil {
		return err
	}
	if ensure {
		if _, err = lock.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
			return err
		}
	}
	var exists bool
	if err := lock.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname=$1 AND has_schema_privilege(oid,'USAGE'))`, schema).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("namespace %q does not exist or is inaccessible (use namespace create)", name)
	}
	d := stdlib.OpenDB(*cfg.ConnConfig)
	defer d.Close()
	// Goose uses an unqualified version table, resolved by the namespace search path.
	migrations, err := namespaceMigrations(mustSub(), schema)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, d, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// Do not wait inside pg_advisory_lock: its statement snapshot can prevent the
// owning runner's concurrent expression-index build from completing. Polling
// releases each snapshot before waiting, while retaining the configured bound.
func acquireMigrationLock(ctx context.Context, conn *pgx.Conn, schema string) error {
	var milliseconds int64
	if err := conn.QueryRow(ctx, `SELECT (extract(epoch FROM current_setting('lock_timeout')::interval)*1000)::bigint`).Scan(&milliseconds); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(milliseconds)*time.Millisecond)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var acquired bool
		if err := conn.QueryRow(waitCtx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "looster:migrations:"+schema).Scan(&acquired); err != nil {
			return err
		}
		if acquired {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("namespace migration lock wait: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// namespaceMigrations rebinds fixed function search paths at migration load time.
// Source SQL stays unchanged, preserving existing default migration history.
func namespaceMigrations(source fs.FS, schema string) (fs.FS, error) {
	return namespaceMigrationFS{FS: source, schema: schema}, nil
}

type namespaceMigrationFS struct {
	fs.FS
	schema string
}

type namespaceMigrationFile struct {
	fs.File
	reader *strings.Reader
}

func (f *namespaceMigrationFile) Read(p []byte) (int, error) { return f.reader.Read(p) }

func (f namespaceMigrationFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.IsDir() {
		return file, nil
	}
	data, err := io.ReadAll(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	sql := strings.ReplaceAll(string(data), "search_path=public,pg_temp", "search_path="+pgx.Identifier{f.schema}.Sanitize()+",pg_temp")
	// Sequence serialization is local to a namespace as well.
	if f.schema != "public" {
		sql = strings.ReplaceAll(sql, "'looster:event-sequence'", "'"+f.schema+":looster:event-sequence'")
	}
	return &namespaceMigrationFile{File: file, reader: strings.NewReader(sql)}, nil
}

// ListNamespaces discovers only looster-owned naming conventions, not unrelated schemas.
func ListNamespaces(ctx context.Context, url string) ([]string, error) {
	cfg, err := parsePoolConfig(url)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE (nspname='public' OR left(nspname,length($1))=$1) AND has_schema_privilege(oid,'USAGE') ORDER BY nspname`, namespaceSchemaPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, err
		}
		name := namespace.Default
		if schema != "public" {
			name = strings.TrimPrefix(schema, namespaceSchemaPrefix)
		}
		if namespace.Validate(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, rows.Err()
}
