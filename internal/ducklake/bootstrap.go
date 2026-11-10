package ducklake

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Release archive SHA256 digests published by duckdb/duckdb v1.5.2 on GitHub.
// Do not obtain a replacement digest from the same download at runtime.
var engineArchives = map[string]struct{ name, digest string }{
	"darwin/amd64":  {"osx-universal", "de789904e1b0b5f305d31b88a6fd8bd4f6ed3f63a00d95f8c7a2559a0995a2f1"},
	"darwin/arm64":  {"osx-universal", "de789904e1b0b5f305d31b88a6fd8bd4f6ed3f63a00d95f8c7a2559a0995a2f1"},
	"linux/amd64":   {"linux-amd64", "fc9145affabca627431e73ddaf6b8117e5c192692480c13886f227be202d5d15"},
	"windows/amd64": {"windows-amd64", "d7b4f5774419c2e9eb14cb7361d3488821ef0244f8af461fd2c6fcb6f43bc3e0"},
	"windows/arm64": {"windows-arm64", "7908e22d25e6991f45e895b1613277e2a600eec6721253f45ce10cde6a3ffdaf"},
	"linux/arm64":   {"linux-arm64", "28b15a8d78e6df62f6ec43da6b0e6397dcd28e25ab93d847df3c5c97f59375f5"},
}

// acquireEngine only executes bytes extracted from a verified release archive.
// Cached archives are reverified, and executables are atomically replaced on
// every acquisition so an unverified executable cache is never trusted.
func acquireEngine(ctx context.Context) (string, error) {
	artifact, ok := engineArchives[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", errors.New("automatic DuckDB installation unsupported on this platform; configure ducklake.binary explicitly")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "looster", "duckdb", EngineVersion, artifact.name)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	archive := filepath.Join(dir, "release.zip")
	data, err := os.ReadFile(archive)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if os.IsNotExist(err) {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/duckdb/duckdb/releases/download/"+EngineVersion+"/duckdb_cli-"+artifact.name+".zip", nil)
		if e != nil {
			return "", e
		}
		client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) > 10 {
				return errors.New("unsafe engine download redirect")
			}
			return nil
		}}
		resp, e := client.Do(req)
		if e != nil {
			return "", errors.New("DuckDB engine download failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", errors.New("DuckDB engine download failed")
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 128<<20))
		if err != nil {
			return "", err
		}
	}
	binary, err := verifiedEngine(data, artifact.digest)
	if err != nil {
		return "", err
	}
	if err = atomicPrivateWrite(archive, data, 0600); err != nil {
		return "", err
	}
	name := "duckdb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err = atomicPrivateWrite(path, binary, 0700); err != nil {
		return "", err
	}
	return path, nil
}
func verifiedEngine(data []byte, digest string) ([]byte, error) {
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != digest {
		return nil, errors.New("DuckDB release checksum mismatch; refusing execution")
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name != "duckdb" && f.Name != "duckdb.exe" {
			continue
		}
		if f.UncompressedSize64 > 256<<20 {
			return nil, errors.New("DuckDB executable too large")
		}
		src, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(src, 256<<20))
		ce := src.Close()
		if e == nil {
			e = ce
		}
		return b, e
	}
	return nil, errors.New("DuckDB release executable missing")
}
func atomicPrivateWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".bootstrap-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
