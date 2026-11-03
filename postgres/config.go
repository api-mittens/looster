package postgres

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgx reads the native libpq environment itself. Only translate underscore
// aliases here, keeping explicit connection settings and native names stronger.
var pgAliases = []struct {
	standard  string
	parameter string
	aliases   []string
}{
	{"PGHOST", "host", []string{"PG_HOST"}},
	{"PGPORT", "port", []string{"PG_PORT"}},
	{"PGUSER", "user", []string{"PG_USER"}},
	{"PGPASSWORD", "password", []string{"PG_PASS", "PG_PASSWORD"}},
	{"PGDATABASE", "dbname", []string{"PG_DATABASE", "PG_DB"}},
	{"PGSSLMODE", "sslmode", []string{"PG_SSLMODE", "PG_SSL_MODE"}},
	{"PGSSLCERT", "sslcert", []string{"PG_SSLCERT"}},
	{"PGSSLKEY", "sslkey", []string{"PG_SSLKEY"}},
	{"PGSSLROOTCERT", "sslrootcert", []string{"PG_SSLROOTCERT"}},
	{"PGSSLPASSWORD", "sslpassword", []string{"PG_SSLPASSWORD"}},
	{"PGPASSFILE", "passfile", []string{"PG_PASSFILE"}},
	{"PGCONNECT_TIMEOUT", "connect_timeout", []string{"PG_CONNECT_TIMEOUT"}},
	{"PGAPPNAME", "application_name", []string{"PG_APPNAME"}},
}

func parsePoolConfig(raw string) (*pgxpool.Config, error) {
	connString, err := connectionString(raw, os.Getenv)
	if err != nil {
		return nil, err
	}
	return pgxpool.ParseConfig(connString)
}

func connectionString(raw string, getenv func(string) string) (string, error) {
	values := url.Values{}
	for _, entry := range pgAliases {
		if getenv(entry.standard) != "" {
			continue
		}
		for _, alias := range entry.aliases {
			if value := getenv(alias); value != "" {
				values.Set(entry.parameter, value)
				break
			}
		}
	}
	if len(values) == 0 {
		return raw, nil
	}
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", errors.New("invalid PostgreSQL connection URL")
		}
		// URL authority/path values, and explicit query parameters, override aliases.
		if u.User != nil {
			if u.User.Username() != "" {
				values.Del("user")
			}
			if _, present := u.User.Password(); present {
				values.Del("password")
			}
		}
		if strings.TrimLeft(u.Path, "/") != "" {
			values.Del("dbname")
		}
		for _, host := range strings.Split(u.Host, ",") {
			if host == "" {
				continue
			}
			if net.ParseIP(strings.Trim(host, "[]")) != nil || !strings.Contains(host, ":") {
				values.Del("host")
				continue
			}
			h, p, err := net.SplitHostPort(host)
			if err != nil {
				return "", errors.New("invalid PostgreSQL URL host or port")
			}
			if h != "" {
				values.Del("host")
			}
			if p != "" {
				values.Del("port")
			}
		}
		query := u.Query()
		for key, value := range values {
			if query.Has(key) || (key == "dbname" && query.Has("database")) {
				continue
			}
			query[key] = value
		}
		u.RawQuery = query.Encode()
		return u.String(), nil
	}
	// pgx processes duplicate keyword parameters from left to right, so the
	// caller's connection string overrides these fallback values. Escape values
	// as data, including passwords containing quotes, backslashes or whitespace.
	var prefix strings.Builder
	for _, entry := range pgAliases {
		if !values.Has(entry.parameter) {
			continue
		}
		value := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(values.Get(entry.parameter))
		prefix.WriteString(entry.parameter + "='" + value + "' ")
	}
	return prefix.String() + raw, nil
}
