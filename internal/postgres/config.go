package postgres

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgx reads native libpq variables itself. Translate underscore aliases while
// keeping explicit connection settings and native names stronger. Credential
// files are separately forbidden, and implicit file discovery is disabled.
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
	{"PGCONNECT_TIMEOUT", "connect_timeout", []string{"PG_CONNECT_TIMEOUT"}},
	{"PGAPPNAME", "application_name", []string{"PG_APPNAME"}},
}

func parsePoolConfig(raw string) (*pgxpool.Config, error) {
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE", "PGPASSFILE", "PG_PASSFILE"} {
		if os.Getenv(key) != "" {
			return nil, fmt.Errorf("PostgreSQL environment key %s is not supported; credentials must be environment-only", key)
		}
	}
	if err := rejectCredentialFiles(raw); err != nil {
		return nil, err
	}
	connString, err := connectionString(raw, os.Getenv)
	if err != nil {
		return nil, err
	}
	// Supply application defaults as fallback connection parameters, not caps on
	// the parsed config. Explicit URL/keyword parameters (including those in an
	// environment-provided connection string) must retain their exact values.
	// Keep pgx's connection capacity unchanged: worker leases hold connections.
	if strings.HasPrefix(connString, "postgres://") || strings.HasPrefix(connString, "postgresql://") {
		u, err := url.Parse(connString)
		if err != nil {
			return nil, errors.New("invalid PostgreSQL connection URL")
		}
		query := u.Query()
		if !query.Has("pool_max_conn_idle_time") {
			query.Set("pool_max_conn_idle_time", "30s")
		}
		if !query.Has("pool_health_check_period") {
			query.Set("pool_health_check_period", "10s")
		}
		u.RawQuery = query.Encode()
		connString = u.String()
	} else {
		connString = "pool_max_conn_idle_time=30s pool_health_check_period=10s " + connString
	}
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection settings (values omitted)")
	}
	return cfg, nil
}

// Scan only parameter names, respecting libpq quoting and escaping so password
// text that resembles a parameter cannot be mistaken for one. pgx still does
// the full syntax validation, with any resulting error sanitized by our caller.
func rejectCredentialFiles(raw string) error {
	check := func(key string) error {
		switch strings.ToLower(key) {
		case "service", "servicefile", "passfile":
			return fmt.Errorf("PostgreSQL connection key %s is not supported; credentials must be environment-only", strings.ToLower(key))
		}
		return nil
	}
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return errors.New("invalid PostgreSQL connection URL")
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return errors.New("invalid PostgreSQL connection URL")
		}
		for _, key := range []string{"service", "servicefile", "passfile"} {
			for actual := range query {
				if strings.EqualFold(actual, key) {
					return check(actual)
				}
			}
		}
		return nil
	}
	space := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f' }
	for i := 0; i < len(raw); {
		for i < len(raw) && space(raw[i]) {
			i++
		}
		start := i
		for i < len(raw) && raw[i] != '=' && !space(raw[i]) {
			i++
		}
		key := raw[start:i]
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i == len(raw) || raw[i] != '=' {
			break
		}
		if err := check(key); err != nil {
			return err
		}
		i++
		for i < len(raw) && space(raw[i]) {
			i++
		}
		quoted := i < len(raw) && raw[i] == '\''
		if quoted {
			i++
		}
		for i < len(raw) {
			if raw[i] == '\\' {
				i += 2
				continue
			}
			if quoted && raw[i] == '\'' {
				i++
				break
			}
			if !quoted && space(raw[i]) {
				break
			}
			i++
		}
	}
	return nil
}

func connectionString(raw string, getenv func(string) string) (string, error) {
	// Override pgx's implicit .pgpass and TLS identity discovery. Explicit
	// environment paths (including underscore aliases) still win below.
	values := url.Values{"passfile": {os.DevNull}}
	for _, key := range []string{"sslcert", "sslkey", "sslrootcert"} {
		values.Set(key, "")
	}
	for _, entry := range pgAliases {
		if value := getenv(entry.standard); value != "" {
			if values.Has(entry.parameter) {
				values.Set(entry.parameter, value)
			}
			continue
		}
		for _, alias := range entry.aliases {
			if value := getenv(alias); value != "" {
				values.Set(entry.parameter, value)
				break
			}
		}
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
	prefix.WriteString("passfile='" + os.DevNull + "' ")
	for _, entry := range pgAliases {
		if !values.Has(entry.parameter) {
			continue
		}
		value := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(values.Get(entry.parameter))
		prefix.WriteString(entry.parameter + "='" + value + "' ")
	}
	return prefix.String() + raw, nil
}
