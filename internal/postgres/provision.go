package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConfigureDatabases creates missing application and optional catalog databases,
// then applies the existing default-namespace application migrations. Empty
// applicationName preserves the resolved database, falling back to looster only
// when none is specified. Empty catalogName disables catalog provisioning.
//
// selectedRaw retains the original connection settings and environment-based
// credentials, changing only the database. It is not safe to log. Persist the
// selected database in deployment configuration before subsequent runtime use.
// Creation and migrations are not atomic. On failure selectedRaw is still
// returned after selection, so callers can report partial progress and retry.
func ConfigureDatabases(ctx context.Context, raw, applicationName, catalogName string) (selectedRaw string, err error) {
	return configureDatabases(ctx, raw, applicationName, catalogName, connectProvisioning, Migrate)
}

type provisioningConn interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Close(context.Context) error
}

type provisioningConnect func(context.Context, *pgx.ConnConfig) (provisioningConn, error)

func connectProvisioning(ctx context.Context, cfg *pgx.ConnConfig) (provisioningConn, error) {
	return pgx.ConnectConfig(ctx, cfg)
}

func selectDatabase(raw, applicationName, catalogName string) (string, *pgxpool.Config, error) {
	cfg, err := parsePoolConfig(raw)
	if err != nil {
		return "", nil, err
	}
	name := applicationName
	if name == "" {
		name = cfg.ConnConfig.Database
	}
	if name == "" {
		name = "looster"
	}
	for _, value := range []string{name, catalogName} {
		if value == "" {
			continue
		}
		// PostgreSQL identifiers are limited to 63 bytes. Reject rather than silently
		// truncate names, which could select or collide with an unrelated database.
		if len(value) > 63 || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
			return "", nil, errors.New("database name must be valid UTF-8, at most 63 bytes, and contain no NUL")
		}
	}
	if catalogName == name {
		return "", nil, errors.New("catalog database must differ from application database")
	}
	selected := raw
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", nil, errors.New("invalid PostgreSQL connection URL")
		}
		q := u.Query()
		q.Del("database")
		q.Set("dbname", name)
		u.RawQuery = q.Encode()
		selected = u.String()
	} else {
		selected += " dbname='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(name) + "'"
	}
	cfg.ConnConfig.Database = name
	return selected, cfg, nil
}

func configureDatabases(ctx context.Context, raw, applicationName, catalogName string, connect provisioningConnect, migrate func(context.Context, string) error) (string, error) {
	selected, cfg, err := selectDatabase(raw, applicationName, catalogName)
	if err != nil {
		return "", err
	}
	application := cfg.ConnConfig.Database
	maintenance := cfg.ConnConfig.Copy()
	maintenance.Database = "postgres"
	conn, err := connect(ctx, maintenance)
	if err != nil {
		return selected, provisioningError(ctx, "connect to maintenance database postgres on the configured server (check CONNECT permission)", err)
	}
	defer conn.Close(context.Background())
	for _, name := range []string{application, catalogName} {
		if name == "" {
			continue
		}
		if err := ensureDatabase(ctx, conn, name); err != nil {
			return selected, err
		}
	}
	if err := migrate(ctx, selected); err != nil {
		return selected, provisioningError(ctx, "migrate application database (provisioned databases retained; retry safely)", err)
	}
	return selected, nil
}

func ensureDatabase(ctx context.Context, conn provisioningConn, name string) error {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil {
		return provisioningError(ctx, "check database existence", err)
	}
	if exists {
		return nil
	}
	_, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "42P04" || pgErr.Code == "23505") {
		// Concurrent CREATE DATABASE can report duplicate_database or a unique
		// catalog-index conflict. Confirm the intended database actually exists.
		if checkErr := conn.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_database WHERE datname=$1)", name).Scan(&exists); checkErr == nil && exists {
			return nil
		}
	}
	return provisioningError(ctx, "create missing database (grant CREATEDB or ask an administrator to create it)", err)
}

// Never include driver/server error text: connection errors can serialize a
// password-bearing URL, and server details can echo user-supplied settings.
func provisioningError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("PostgreSQL %s: %w", operation, ctx.Err())
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) == 5 {
		return fmt.Errorf("PostgreSQL %s failed (SQLSTATE %s; values omitted)", operation, pgErr.Code)
	}
	return fmt.Errorf("PostgreSQL %s failed (details omitted)", operation)
}
