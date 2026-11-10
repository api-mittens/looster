// Package ducklake bootstraps a pinned DuckDB CLI with signed extensions.
package ducklake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/objectstore"
)

const EngineVersion = "v1.5.2"
const DuckLakeVersion = "1.0"

// Official core extension build IDs verified against EngineVersion. Runtime
// requires these exact signed artifacts, not a moving extension release label.
const DuckLakeExtensionVersion = "415a9ebd"
const PostgresExtensionVersion = "c89234f"
const HTTPFSExtensionVersion = "13e18b3"

// Config must not be logged or serialized. CatalogConnection is a fully resolved
// libpq KEY=VALUE string from the caller's PostgreSQL resolver, not a pgx TLS
// config translated back to a URL. Explicit sslmode and identity/root settings
// are passed through unchanged. Ambient PG variables and credential files are
// not used. Namespace selects an isolated metadata schema.
type Config struct {
	Binary            string
	CatalogConnection string
	Namespace         string
	DataPath          string
	S3                objectstore.Config
}

func (c Config) String() string   { return "ducklake.Config{values redacted}" }
func (c Config) GoString() string { return c.String() }

// Lake retains credentials in memory only. Each Exec uses a fresh in-memory
// DuckDB and reattaches the catalog, so transactions must fit in one Exec call.
// The lake alias is always "lake". Do not log Config or supplied SQL.
type Lake struct {
	prelude string
	binary  string
	run     runner
}

func (l *Lake) String() string   { return "ducklake.Lake{values redacted}" }
func (l *Lake) GoString() string { return l.String() }

type runner func(context.Context, string, string) error

// Validate checks configuration without starting DuckDB or contacting services.
func Validate(c Config) error { _, err := prepare(c); return err }

// Open initializes or attaches metadata. It does not prove S3 write access.
func Open(ctx context.Context, c Config) (*Lake, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	if c.Binary == "" {
		binary, err := acquireEngine(ctx)
		if err != nil {
			return nil, err
		}
		c.Binary = binary
	}
	// Official signed extensions only, with no insecure transport fallback.
	install, err := extensionInstallSQL(ctx)
	if err != nil {
		return nil, err
	}
	if err := runCLI(ctx, c.Binary, install); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("DuckDB signed extension bootstrap failed (details suppressed)")
	}
	return open(ctx, c, runCLI)
}
func open(ctx context.Context, c Config, run runner) (*Lake, error) {
	p, err := prepare(c)
	if err != nil {
		return nil, err
	}
	l := &Lake{p, c.Binary, run}
	if err = l.Exec(ctx, ""); err != nil {
		return nil, err
	}
	return l, nil
}

// Exec executes trusted application SQL. Output and engine diagnostics are
// intentionally discarded because both can contain credentials or connection
// strings. Context cancellation terminates the child process.
func (l *Lake) Exec(ctx context.Context, sql string) error {
	if err := l.run(ctx, l.binary, l.prelude+"\n"+sql); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("DuckLake operation failed (details suppressed): check pinned engine/extensions, catalog TLS/permissions and S3 settings")
	}
	return nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func prepare(c Config) (string, error) {
	if c.Namespace == "" {
		c.Namespace = namespace.Default
	}
	if err := namespace.Validate(c.Namespace); err != nil {
		return "", err
	}
	if strings.IndexByte(c.CatalogConnection, 0) >= 0 || !validConnection(c.CatalogConnection) {
		return "", errors.New("DuckLake requires fully resolved libpq keyword settings with explicit host, dbname, user and sslmode; unsupported or file-provider settings rejected")
	}
	u, err := url.Parse(c.DataPath)
	if err != nil || u.Scheme != "s3" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.Path == "/" {
		return "", errors.New("DuckLake requires an s3://bucket/dedicated-prefix/ data path")
	}
	s := c.S3
	if s.Bucket != "" && s.Bucket != u.Host {
		return "", errors.New("DuckLake S3 bucket and data path disagree")
	}
	if s.AccessKey == "" && s.SecretKey == "" && s.SessionToken == "" {
		s.AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
		s.SecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
		s.SessionToken = os.Getenv("AWS_SESSION_TOKEN")
	}
	if s.AccessKey == "" || s.SecretKey == "" {
		return "", errors.New("DuckLake S3 requires explicit access and secret keys, or AWS environment credentials")
	}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	endpoint := ""
	if s.Endpoint != "" {
		e, err := url.Parse(s.Endpoint)
		if err != nil || (e.Scheme != "http" && e.Scheme != "https") || e.Host == "" || e.User != nil || (e.Path != "" && e.Path != "/") || e.RawQuery != "" || e.Fragment != "" {
			return "", errors.New("unsupported DuckLake S3 endpoint")
		}
		endpoint = ", ENDPOINT " + quote(e.Host) + ", USE_SSL "
		if e.Scheme == "https" {
			endpoint += "true"
		} else {
			endpoint += "false"
		}
	}
	// Temporary secrets, no persistent secret storage. Never emit this SQL.
	return "SELECT CASE WHEN version() = '" + EngineVersion + "' THEN true ELSE error('engine version mismatch') END;\n" +
		"SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;\nLOAD ducklake; LOAD postgres; LOAD httpfs;\n" +
		"SELECT CASE WHEN (SELECT count(*) FROM duckdb_extensions() WHERE loaded AND ((extension_name='ducklake' AND extension_version='" + DuckLakeExtensionVersion + "') OR (extension_name='postgres_scanner' AND extension_version='" + PostgresExtensionVersion + "') OR (extension_name='httpfs' AND extension_version='" + HTTPFSExtensionVersion + "'))) = 3 THEN true ELSE error('extension version mismatch') END;\n" +
		"CREATE TEMPORARY SECRET looster_s3 (TYPE s3, PROVIDER config, KEY_ID " + quote(s.AccessKey) + ", SECRET " + quote(s.SecretKey) + ", SESSION_TOKEN " + quote(s.SessionToken) + ", REGION " + quote(s.Region) + ", URL_STYLE 'path', SCOPE " + quote(c.DataPath) + endpoint + ");\n" +
		"CREATE TEMPORARY SECRET looster_lake (TYPE ducklake, METADATA_PATH " + quote("postgres:"+c.CatalogConnection+" passfile='/dev/null'") + ", DATA_PATH " + quote(c.DataPath) + ");\n" +
		"ATTACH 'ducklake:looster_lake' AS lake (METADATA_SCHEMA " + quote(metadataSchema(c.Namespace)) + ", DATA_INLINING_ROW_LIMIT 0, OVERRIDE_DATA_PATH false);\n", nil
}

// validConnection accepts only libpq keyword syntax and known parameters. It
// deliberately rejects URLs, services and unsupported pgx-only options rather
// than weakening TLS or changing their meaning. Values are not reconstructed.
func validConnection(raw string) bool { _, ok := connectionSettings(raw); return ok }

func connectionSettings(raw string) (map[string]string, bool) {
	allowed := strings.Fields("host hostaddr port dbname user password sslmode sslcert sslkey sslrootcert sslpassword sslcrl sslcrldir sslsni ssl_min_protocol_version ssl_max_protocol_version connect_timeout application_name target_session_attrs channel_binding options")
	keys := map[string]bool{}
	for _, k := range allowed {
		keys[k] = true
	}
	seen := map[string]string{}
	s := raw
	for strings.TrimSpace(s) != "" {
		s = strings.TrimSpace(s)
		i := strings.IndexByte(s, '=')
		if i < 1 {
			return nil, false
		}
		k := strings.TrimSpace(s[:i])
		if !keys[k] {
			return nil, false
		}
		s = strings.TrimLeft(s[i+1:], " \t\r\n")
		value := ""
		quoted := strings.HasPrefix(s, "'")
		if quoted {
			s = s[1:]
		}
		closed := !quoted
		for len(s) > 0 {
			b := s[0]
			s = s[1:]
			if b == '\\' {
				if len(s) == 0 {
					return nil, false
				}
				value += s[:1]
				s = s[1:]
				continue
			}
			if quoted && b == '\'' {
				closed = true
				break
			}
			if !quoted && strings.ContainsRune(" \t\r\n", rune(b)) {
				break
			}
			value += string(b)
		}
		if !closed {
			return nil, false
		}
		if quoted && len(s) > 0 && !strings.ContainsRune(" \t\r\n", rune(s[0])) {
			return nil, false
		}
		seen[k] = value
	}
	return seen, seen["host"] != "" && seen["dbname"] != "" && seen["user"] != "" && seen["sslmode"] != ""
}

// CatalogIdentity is an opaque spool identity independent of authentication and
// TLS settings, so credential rotation does not strand acknowledged payloads.
func CatalogIdentity(c Config) (string, error) {
	settings, ok := connectionSettings(c.CatalogConnection)
	if !ok {
		return "", errors.New("invalid DuckLake catalog settings")
	}
	h := sha256.Sum256([]byte(settings["host"] + "\x00" + settings["hostaddr"] + "\x00" + settings["port"] + "\x00" + settings["dbname"] + "\x00" + c.DataPath + "\x00" + c.Namespace))
	return fmt.Sprintf("%x", h[:16]), nil
}

func metadataSchema(ns string) string {
	h := sha256.Sum256([]byte(ns))
	return fmt.Sprintf("looster_ducklake_%x", h[:16])
}

func runCLI(ctx context.Context, binary, sql string) error {
	// -init /dev/null prevents ~/.duckdbrc, -batch avoids interactive history.
	// Secrets are only on stdin, never argv or inherited credential variables.
	cmd := exec.CommandContext(ctx, binary, "-init", "/dev/null", "-batch", "-bail", ":memory:")
	cmd.Stdin = strings.NewReader(sql)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	for _, key := range []string{"HOME", "PATH", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	return cmd.Run()
}

// ProbeDisposable writes a real Parquet-backed table, exits the writer process,
// reopens and checks rows, then drops the table and physically cleans files.
// It is ONLY for an exclusively owned disposable catalog and S3 prefix: cleanup
// expires catalog-wide history. Never call against production or shared storage.
// A failed probe may leave artifacts, and returns a sanitized error for retry.
func ProbeDisposable(ctx context.Context, c Config, confirmDisposable bool) error {
	if !confirmDisposable {
		return errors.New("DuckLake probe requires explicit disposable catalog and prefix confirmation")
	}
	l, err := Open(ctx, c)
	if err != nil {
		return err
	}
	return l.probe(ctx)
}
func (l *Lake) probe(ctx context.Context) error {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return errors.New("DuckLake probe random ID unavailable")
	}
	tableName := "looster_probe_" + hex.EncodeToString(b[:])
	table := "lake.main." + tableName
	if err := l.Exec(ctx, "CREATE TABLE "+table+" (id INTEGER); CALL lake.set_option('data_inlining_row_limit', 0, table_name => "+quote(tableName)+"); INSERT INTO "+table+" VALUES (42);"); err != nil {
		return err
	}
	// array_agg requires row values, unlike count/min that can be answered from
	// Parquet/catalog statistics without proving that the file is readable.
	if err := l.Exec(ctx, "SELECT CASE WHEN array_agg(id)=[42] THEN true ELSE error('probe mismatch') END FROM "+table+";"); err != nil {
		return err
	}
	return l.Exec(ctx, "DROP TABLE "+table+"; CALL ducklake_expire_snapshots('lake', older_than => now()); CALL ducklake_cleanup_old_files('lake', cleanup_all => true);")
}
