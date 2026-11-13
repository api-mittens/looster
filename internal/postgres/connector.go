package postgres

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Resolve settings as text, not by serializing pgx TLSConfig: TLSConfig no
// longer records sslmode or the original certificate/root paths. Native libpq
// connectors must receive those exact inputs to retain verification semantics.
func databaseConnectionString(raw, name string) (string, error) {
	selected, cfg, err := selectDatabase(raw, name, "")
	if err != nil {
		return "", err
	}
	resolved, err := connectionString(selected, os.Getenv)
	if err != nil {
		return "", err
	}
	values := map[string]string{
		"host": cfg.ConnConfig.Host, "port": strconv.Itoa(int(cfg.ConnConfig.Port)),
		"user": cfg.ConnConfig.User, "password": cfg.ConnConfig.Password,
		"dbname": cfg.ConnConfig.Database, "sslmode": "prefer",
		"sslcert": "", "sslkey": "", "sslrootcert": "",
	}
	for _, entry := range pgAliases {
		if value := os.Getenv(entry.standard); value != "" {
			values[entry.parameter] = value
		}
	}
	for env, key := range map[string]string{"PGSSLSNI": "sslsni", "PGTARGETSESSIONATTRS": "target_session_attrs", "PGCHANNELBINDING": "channel_binding", "PGOPTIONS": "options"} {
		if value := os.Getenv(env); value != "" {
			values[key] = value
		}
	}
	for _, env := range []string{"PGTZ", "PGSSLNEGOTIATION", "PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION", "PGREQUIREAUTH"} {
		if os.Getenv(env) != "" {
			return "", errors.New("unsupported PostgreSQL connector environment settings (values omitted)")
		}
	}
	explicit, err := connectorSettings(resolved)
	if err != nil {
		return "", err
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("host port dbname user password sslmode sslcert sslkey sslrootcert sslpassword sslsni ssl_min_protocol_version ssl_max_protocol_version connect_timeout application_name target_session_attrs channel_binding options") {
		allowed[key] = true
	}
	for key, value := range explicit {
		// selectDatabase already validates these with pgxpool.ParseConfig.
		// They control the Go pool, not the native DuckLake connection.
		switch key {
		case "pool_max_conns", "pool_min_conns", "pool_min_idle_conns",
			"pool_max_conn_lifetime", "pool_max_conn_idle_time",
			"pool_health_check_period", "pool_max_conn_lifetime_jitter":
			continue
		}
		if key == "database" {
			key = "dbname"
		}
		if key == "passfile" && value == os.DevNull {
			continue
		}
		if !allowed[key] {
			return "", errors.New("unsupported PostgreSQL connector connection settings (values omitted)")
		}
		values[key] = value
	}
	if values["sslmode"] == "" {
		values["sslmode"] = "prefer"
	}
	// Empty libpq TLS file settings trigger home-directory discovery. Use paths
	// beneath a non-directory device to make discovery impossible instead. Modes
	// requiring a root file cannot safely emulate pgx's implicit system-root pool.
	for _, key := range []string{"sslcert", "sslkey", "sslrootcert"} {
		if values[key] == "" {
			values[key] = filepath.Join(os.DevNull, "looster-no-"+key)
		}
	}
	if (values["sslmode"] == "verify-ca" || values["sslmode"] == "verify-full") && values["sslrootcert"] == filepath.Join(os.DevNull, "looster-no-sslrootcert") {
		return "", errors.New("PostgreSQL connector verification requires an explicit sslrootcert path (values omitted)")
	}
	// Always prefer the validated selected name over environment and aliases.
	values["dbname"] = cfg.ConnConfig.Database
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		if strings.ContainsRune(values[key], 0) {
			return "", errors.New("invalid PostgreSQL connector settings (values omitted)")
		}
		out.WriteString(key + "='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(values[key]) + "' ")
	}
	return strings.TrimSpace(out.String()), nil
}

func connectorSettings(raw string) (map[string]string, error) {
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return connectorKeywordSettings(raw, true)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connector settings (values omitted)")
	}
	values := map[string]string{}
	if u.User != nil {
		if user := u.User.Username(); user != "" {
			values["user"] = user
		}
		if password, ok := u.User.Password(); ok {
			values["password"] = password
		}
	}
	var hosts, ports []string
	for _, host := range strings.Split(u.Host, ",") {
		if host == "" {
			continue
		}
		if net.ParseIP(strings.Trim(host, "[]")) != nil || !strings.Contains(host, ":") {
			hosts = append(hosts, strings.Trim(host, "[]"))
			continue
		}
		h, p, err := net.SplitHostPort(host)
		if err != nil {
			return nil, errors.New("invalid PostgreSQL connector host settings (values omitted)")
		}
		if h != "" {
			hosts = append(hosts, h)
		}
		if p != "" {
			ports = append(ports, p)
		}
	}
	if len(ports) > 0 && len(ports) != len(hosts) {
		return nil, errors.New("PostgreSQL connector requires an explicit port for every URL host when any port is specified (values omitted)")
	}
	if len(hosts) > 0 {
		values["host"] = strings.Join(hosts, ",")
	}
	if len(ports) > 0 {
		values["port"] = strings.Join(ports, ",")
	}
	if database := strings.TrimLeft(u.Path, "/"); database != "" {
		values["dbname"] = database
	}
	for key, list := range u.Query() {
		if key == "database" {
			key = "dbname"
		}
		values[key] = list[0]
	}
	return values, nil
}

// The input has already passed pgx validation. Scan keyword values as data,
// respecting quotes and escapes so password text cannot inject parameters.
func connectorKeywords(raw string) (map[string]string, error) {
	return connectorKeywordSettings(raw, false)
}

func connectorKeywordSettings(raw string, pgxSource bool) (map[string]string, error) {
	values := map[string]string{}
	space := func(b byte) bool { return strings.ContainsRune(" \t\n\r\v\f", rune(b)) }
	invalid := func() (map[string]string, error) {
		return nil, errors.New("invalid PostgreSQL connector settings (values omitted)")
	}
	for i := 0; i < len(raw); {
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i == len(raw) {
			break
		}
		start := i
		for i < len(raw) && raw[i] != '=' && !space(raw[i]) {
			i++
		}
		key := raw[start:i]
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if key == "" || i == len(raw) || raw[i] != '=' {
			return invalid()
		}
		i++
		for i < len(raw) && space(raw[i]) {
			i++
		}
		quoted := i < len(raw) && raw[i] == '\''
		if quoted {
			i++
		}
		closed := !quoted
		var value strings.Builder
		for i < len(raw) {
			b := raw[i]
			i++
			if b == '\\' {
				if i == len(raw) {
					return invalid()
				}
				if pgxSource {
					value.WriteByte('\\')
				}
				value.WriteByte(raw[i])
				i++
				continue
			}
			if quoted && b == '\'' {
				closed = true
				break
			}
			if !quoted && space(b) {
				break
			}
			value.WriteByte(b)
		}
		if !closed {
			return invalid()
		}
		if quoted && i < len(raw) && !space(raw[i]) {
			return invalid()
		}
		if key == "database" {
			key = "dbname"
		}
		text := value.String()
		if pgxSource {
			// pgx decodes only escaped backslash and quote in input DSNs. The
			// serializer then escapes every backslash for native libpq, which consumes
			// a backslash before any byte. Keep the two grammars distinct.
			text = strings.ReplaceAll(strings.ReplaceAll(text, `\\`, `\`), `\'`, `'`)
		}
		values[key] = text
	}
	return values, nil
}
