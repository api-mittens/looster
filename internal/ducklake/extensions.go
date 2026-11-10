package ducklake

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Download with Go's verified HTTPS transport. DuckDB needs httpfs to perform
// HTTPS installs itself, so relying on INSTALL remote would require an insecure
// HTTP bootstrap. Absolute LOAD retains DuckDB's signature verification and
// never changes the user's normal extension installation directory.
func extensionLoadSQL(ctx context.Context) (string, error) {
	platform := ""
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		platform = "osx_arm64"
	case "darwin/amd64":
		platform = "osx_amd64"
	case "linux/arm64":
		platform = "linux_arm64"
	case "windows/amd64":
		platform = "windows_amd64"
	case "windows/arm64":
		platform = "windows_arm64"
	case "linux/amd64":
		platform = "linux_amd64"
	default:
		return "", errors.New("DuckLake extensions unsupported on this platform")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "looster", "duckdb", EngineVersion, platform, "extensions")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	sql := "SELECT CASE WHEN version() = '" + EngineVersion + "' THEN true ELSE error('engine version mismatch') END; SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;"
	for _, name := range []string{"httpfs", "postgres_scanner", "ducklake"} {
		path := filepath.Join(dir, name+".duckdb_extension")
		if _, err = os.Stat(path); os.IsNotExist(err) {
			data, e := downloadExtension(ctx, "https://extensions.duckdb.org/"+EngineVersion+"/"+platform+"/"+name+".duckdb_extension.gz")
			if e != nil {
				return "", e
			}
			if e = atomicPrivateWrite(path, data, 0600); e != nil {
				return "", e
			}
		} else if err != nil {
			return "", err
		}
		sql += " LOAD " + quote(path) + ";"
	}
	// Exact build IDs are checked by prepare before catalog access.
	return strings.TrimSpace(sql), nil
}
func downloadExtension(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 10 {
			return errors.New("unsafe extension redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("DuckDB signed extension download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("DuckDB signed extension download failed")
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, errors.New("invalid DuckDB extension archive")
	}
	defer gz.Close()
	data, err := io.ReadAll(io.LimitReader(gz, (256<<20)+1))
	if err != nil || len(data) > 256<<20 {
		return nil, errors.New("invalid DuckDB extension archive")
	}
	return data, nil
}
