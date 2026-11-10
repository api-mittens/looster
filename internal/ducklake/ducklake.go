// Package ducklake embeds the official DuckDB database/sql driver.
// Open attaches metadata but does not prove object storage write access.
package ducklake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/objectstore"
)

// Official compatibility research: duckdb-go's version matrix maps
// v2.10502.0 to DuckDB v1.5.2 (github.com/duckdb/duckdb-go).
// ducklake.select/release_calendar maps DuckDB 1.5.2 to DuckLake 1.0.
// These official HTTPS artifacts were verified with TestOfficialExtensions on
// darwin_arm64. Other platforms must also pass the runtime build-ID gate.
const EngineVersion = "v1.5.2"
const DuckLakeVersion = "1.0"

// EngineMemoryLimit caps DuckDB's buffer manager, not total process RSS.
// Apply it in the native engine configuration before any session is opened.
const EngineMemoryLimit = "256MiB"

// Official core extension build IDs verified against EngineVersion. Runtime
// requires these exact signed artifacts, not a moving extension release label.
const DuckLakeExtensionVersion = "415a9ebd"
const PostgresExtensionVersion = "c89234f"
const HTTPFSExtensionVersion = "13e18b3"

// Config must not be logged or serialized. CatalogConnection is a fully resolved
// libpq KEY=VALUE string from the caller's PostgreSQL resolver, not a pgx TLS
// config translated back to a URL. Explicit sslmode and identity/root settings
// are passed through unchanged. Ambient libpq connection defaults are accepted
// only when overridden by explicit resolved fields. Password files are disabled.
// Namespace selects an isolated metadata schema.
type Config struct {
	CatalogConnection string
	Namespace         string
	DataPath          string
	S3                objectstore.Config
}

func (c Config) String() string   { return "ducklake.Config{values redacted}" }
func (c Config) GoString() string { return c.String() }

// Lake owns one in-memory engine and one pinned SQL session. Call Close.
// Transactions and session state survive Exec calls. Do not log supplied SQL.
type Lake struct {
	gate     chan struct{}
	db       *sql.DB
	conn     *sql.Conn
	cancel   context.CancelFunc
	lifetime context.Context
	closed   bool
}

func (l *Lake) String() string   { return "ducklake.Lake{values redacted}" }
func (l *Lake) GoString() string { return l.String() }

// Validate checks configuration without starting DuckDB or contacting services.
func Validate(c Config) error { _, err := prepare(c); return err }

// Open initializes or attaches metadata. It does not prove S3 write access.
func Open(ctx context.Context, c Config) (*Lake, error) {
	p, err := prepare(c)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, operationError("extension cache location")
	}
	directory := filepath.Join(cache, "looster", "duckdb", EngineVersion)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, operationError("extension cache creation")
	}
	l, err := OpenEngine(ctx)
	if err != nil {
		return nil, err
	}
	if err = l.Exec(ctx, "SET extension_directory="+quote(directory)); err != nil {
		_ = l.Close()
		return nil, err
	}
	if err = l.bootstrapHTTPFS(ctx, directory); err != nil {
		_ = l.Close()
		return nil, err
	}
	// Bootstrap is credential-free. Keep failures distinct from catalog/S3
	// attachment failures without exposing SQL or native error messages.
	phases := strings.SplitN(p, "CREATE TEMPORARY SECRET", 2)
	if err = l.exec(ctx, phases[0], "engine/extension bootstrap"); err == nil {
		err = l.exec(ctx, "CREATE TEMPORARY SECRET"+phases[1], "temporary secrets/catalog attachment")
	}
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// OpenEngine opens a bounded in-memory DuckDB engine and retained SQL session.
// It does not install extensions, attach a catalog, or configure S3. The caller
// owns its lifetime and must Close it, typically after service.Serve returns.
// ctx controls initialization only. Canceling it does not close the engine.
func OpenEngine(ctx context.Context) (*Lake, error) {
	return openEngine(ctx)
}

func openEngine(ctx context.Context) (*Lake, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := sql.Open("duckdb", "?memory_limit="+EngineMemoryLimit)
	if err != nil {
		return nil, operationError("engine initialization")
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, operationError("session initialization")
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Lake{gate: make(chan struct{}, 1), db: db, conn: conn, lifetime: lifetime, cancel: cancel}, nil
}

var ErrClosed = errors.New("DuckLake is closed")

func operationError(stage string) error {
	return errors.New("DuckLake " + stage + " failed (engine details suppressed to protect credentials): check engine/extension compatibility, HTTPS extension access, catalog TLS/permissions and S3 settings")
}

// Exec executes trusted application SQL in the retained session. Engine errors
// are never wrapped because SQL diagnostics can contain credentials and data.
func (l *Lake) Exec(ctx context.Context, query string) error {
	return l.exec(ctx, query, "SQL operation")
}

// withConn retains exclusive session access across a trusted compound operation.
// The callback must release rows, appenders and transactions before returning.
// Raw errors never escape this boundary because they can contain credentials.
func (l *Lake) withConn(ctx context.Context, stage string, consume func(context.Context, *sql.Conn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-l.lifetime.Done():
		return ErrClosed
	}
	defer func() { <-l.gate }()
	if l.closed || l.lifetime.Err() != nil {
		return ErrClosed
	}
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.lifetime, cancel)
	defer stop()
	defer cancel()
	err := consume(op, l.conn)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if l.lifetime.Err() != nil {
		return ErrClosed
	}
	if err != nil {
		return operationError(stage)
	}
	return nil
}

// Query consumes rows while retaining exclusive ownership of the session.
// consume must not retain rows or call another Lake method. All row and callback
// errors are sanitized because native diagnostics and scan errors can hold data.
func (l *Lake) Query(ctx context.Context, query string, consume func(*sql.Rows) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-l.lifetime.Done():
		return ErrClosed
	}
	defer func() { <-l.gate }()
	if l.closed || l.lifetime.Err() != nil {
		return ErrClosed
	}
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.lifetime, cancel)
	defer stop()
	defer cancel()
	rows, err := l.conn.QueryContext(op, query)
	if err == nil {
		defer rows.Close()
		err = consume(rows)
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if l.lifetime.Err() != nil {
		return ErrClosed
	}
	if err != nil {
		return operationError("SQL query")
	}
	return nil
}

func (l *Lake) exec(ctx context.Context, query, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-l.lifetime.Done():
		return ErrClosed
	}
	defer func() { <-l.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.closed || l.lifetime.Err() != nil {
		return ErrClosed
	}
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.lifetime, cancel)
	defer stop()
	defer cancel()
	_, err := l.conn.ExecContext(op, query)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if l.lifetime.Err() != nil {
			return ErrClosed
		}
		return operationError(stage)
	}
	return nil
}

// Close cancels active work before waiting for the session, then releases both
// connection and engine. It is idempotent and safe concurrently with Exec.
func (l *Lake) Close() error {
	l.cancel()
	l.gate <- struct{}{}
	defer func() { <-l.gate }()
	if l.closed {
		return nil
	}
	l.closed = true
	e1 := l.conn.Close()
	e2 := l.db.Close()
	if e1 != nil || e2 != nil {
		return operationError("close")
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
	if err := validateAmbientConnection(c.CatalogConnection); err != nil {
		return "", err
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
		"SET allow_unsigned_extensions=false; SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;\n" +
		"LOAD httpfs;\nINSTALL postgres FROM 'https://extensions.duckdb.org'; LOAD postgres;\nINSTALL ducklake FROM 'https://extensions.duckdb.org'; LOAD ducklake;\n" +
		"SELECT CASE WHEN (SELECT count(*) FROM duckdb_extensions() WHERE loaded AND ((extension_name='ducklake' AND extension_version='" + DuckLakeExtensionVersion + "') OR (extension_name='postgres_scanner' AND extension_version='" + PostgresExtensionVersion + "') OR (extension_name='httpfs' AND extension_version='" + HTTPFSExtensionVersion + "'))) = 3 THEN true ELSE error('extension version mismatch') END;\n" +
		"CREATE TEMPORARY SECRET looster_s3 (TYPE s3, PROVIDER config, KEY_ID " + quote(s.AccessKey) + ", SECRET " + quote(s.SecretKey) + ", SESSION_TOKEN " + quote(s.SessionToken) + ", REGION " + quote(s.Region) + ", URL_STYLE 'path', SCOPE " + quote(c.DataPath) + endpoint + ");\n" +
		"CREATE TEMPORARY SECRET looster_lake (TYPE ducklake, METADATA_PATH " + quote("postgres:"+c.CatalogConnection+" passfile="+libpqQuote(os.DevNull)) + ", DATA_PATH " + quote(c.DataPath) + ");\n" +
		"ATTACH 'ducklake:looster_lake' AS lake (METADATA_SCHEMA " + quote(metadataSchema(c.Namespace)) + ", DATA_INLINING_ROW_LIMIT 0, OVERRIDE_DATA_PATH false);\n", nil
}

// validConnection accepts only libpq keyword syntax and known parameters. It
// deliberately rejects URLs, services and unsupported pgx-only options rather
// than weakening TLS or changing their meaning. Values are not reconstructed.
func validConnection(raw string) bool {
	seen := connectionFields(raw)
	return seen["host"] && seen["dbname"] && seen["user"] && seen["sslmode"]
}

// connectionFields tracks the last value of each keyword, just like libpq.
// A nonempty explicit value overrides the corresponding environment default.
func connectionFields(raw string) map[string]bool {
	settings, _ := connectionSettings(raw)
	fields := map[string]bool{}
	for k, v := range settings {
		fields[k] = v != ""
	}
	return fields
}

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

// CatalogIdentity excludes credentials so rotation does not strand spooled data.
func CatalogIdentity(c Config) (string, error) {
	settings, ok := connectionSettings(c.CatalogConnection)
	if !ok {
		return "", errors.New("invalid DuckLake catalog settings")
	}
	if c.Namespace == "" {
		c.Namespace = namespace.Default
	}
	h := sha256.Sum256([]byte(settings["host"] + "\x00" + settings["hostaddr"] + "\x00" + settings["port"] + "\x00" + settings["dbname"] + "\x00" + c.DataPath + "\x00" + c.Namespace))
	return fmt.Sprintf("%x", h[:16]), nil
}

func metadataSchema(ns string) string {
	h := sha256.Sum256([]byte(ns))
	return fmt.Sprintf("looster_ducklake_%x", h[:16])
}

func libpqQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// ProbeDisposable writes a real Parquet-backed table, closes the writer engine,
// opens a fresh engine and reads rows, then drops the table and cleans files.
// It is ONLY for an exclusively owned disposable catalog and S3 prefix: cleanup
// expires catalog-wide history. Never call against production or shared storage.
// A failed probe may leave artifacts, and returns a sanitized error for retry.
func ProbeDisposable(ctx context.Context, c Config, confirmDisposable bool) error {
	if !confirmDisposable {
		return errors.New("DuckLake probe requires explicit disposable catalog and prefix confirmation")
	}
	return probeDisposable(ctx, c, func(ctx context.Context, c Config) (probeSession, error) { return Open(ctx, c) })
}

// The opener seam allows tests to prove independent engine lifetimes, not just
// repeated execution on the same session.
type probeSession interface {
	Exec(context.Context, string) error
	Close() error
}

func probeDisposable(ctx context.Context, c Config, opener func(context.Context, Config) (probeSession, error)) (result error) {
	l, err := opener(ctx, c)
	if err != nil {
		return err
	}
	defer l.Close()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return errors.New("DuckLake probe random ID unavailable")
	}
	tableName := "looster_probe_" + hex.EncodeToString(b[:])
	table := "lake.main." + tableName
	if err := l.Exec(ctx, "CREATE TABLE "+table+" (id INTEGER); CALL lake.set_option('data_inlining_row_limit', 0, table_name => "+quote(tableName)+"); INSERT INTO "+table+" VALUES (42);"); err != nil {
		return err
	}
	if err := l.Close(); err != nil {
		return err
	}
	reader, err := opener(ctx, c)
	if err != nil {
		return err
	}
	defer func() {
		if err := reader.Close(); result == nil {
			result = err
		}
	}()
	// array_agg requires row values, unlike count/min that can be answered from
	// Parquet/catalog statistics without proving that the file is readable.
	if err := reader.Exec(ctx, "SELECT CASE WHEN array_agg(id)=[42] THEN true ELSE error('probe mismatch') END FROM "+table+";"); err != nil {
		return err
	}
	return reader.Exec(ctx, "DROP TABLE "+table+"; CALL ducklake_expire_snapshots('lake', older_than => now()); CALL ducklake_cleanup_old_files('lake', cleanup_all => true);")
}
