package ducklake

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const extensionRepository = "https://extensions.duckdb.org"

// DuckDB needs httpfs to read HTTPS, including an HTTPS INSTALL of httpfs
// itself. Bootstrap that one signed artifact with Go HTTPS instead of silently
// falling back to HTTP. Native INSTALL/LOAD still verifies its signature.
func (l *Lake) bootstrapHTTPFS(ctx context.Context, directory string) error {
	fail := func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return operationError("signed HTTPS httpfs bootstrap")
	}
	if err := l.exec(ctx, "SELECT CASE WHEN version()='"+EngineVersion+"' THEN true ELSE error('engine version mismatch') END; SET allow_unsigned_extensions=false; SET autoinstall_known_extensions=false; SET autoload_known_extensions=false", "engine bootstrap"); err != nil {
		return err
	}
	var installed bool
	if err := l.conn.QueryRowContext(ctx, "SELECT installed FROM duckdb_extensions() WHERE extension_name='httpfs'").Scan(&installed); err != nil {
		return fail()
	}
	if installed {
		return l.exec(ctx, "LOAD httpfs", "cached signed httpfs load")
	}
	var platform string
	if err := l.conn.QueryRowContext(ctx, "PRAGMA platform").Scan(&platform); err != nil {
		return fail()
	}
	switch platform {
	case "osx_arm64", "osx_amd64", "linux_amd64", "linux_arm64", "linux_amd64_gcc4", "windows_amd64", "windows_arm64":
	default:
		return operationError("unsupported official extension platform")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, extensionRepository+"/"+EngineVersion+"/"+platform+"/httpfs.duckdb_extension.gz", nil)
	if err != nil {
		return fail()
	}
	client := &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 5 {
			return operationError("HTTPS extension redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return fail()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fail()
	}
	archive, err := gzip.NewReader(response.Body)
	if err != nil {
		return fail()
	}
	defer archive.Close()
	temporary, err := os.MkdirTemp(directory, "httpfs-bootstrap-")
	if err != nil {
		return fail()
	}
	defer os.RemoveAll(temporary)
	path := filepath.Join(temporary, "httpfs.duckdb_extension")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail()
	}
	const maxArtifactBytes = 256 << 20
	written, copyErr := io.Copy(file, io.LimitReader(archive, maxArtifactBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > maxArtifactBytes {
		return fail()
	}
	if err := l.exec(ctx, "INSTALL "+quote(path)+"; LOAD httpfs", "signed httpfs installation"); err != nil {
		return err
	}
	return nil
}
