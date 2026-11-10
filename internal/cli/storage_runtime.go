package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/objectstore"
	"github.com/api-mittens/looster/internal/postgres"
)

// runtimeLakeConfig derives the catalog from the same resolved PostgreSQL
// settings as the application. No separate database or CREATEDB is required.
func (a *application) runtimeLakeConfig(ns string) (ducklake.Config, error) {
	if err := namespace.Validate(ns); err != nil {
		return ducklake.Config{}, err
	}
	conn, err := postgres.DatabaseConnectionString(a.v.GetString("database_url"), a.v.GetString("ducklake.database"))
	if err != nil {
		return ducklake.Config{}, err
	}
	bucket := a.v.GetString("s3.bucket")
	if !validDuckLakeBucket(bucket) {
		return ducklake.Config{}, fmt.Errorf("DuckLake requires a valid configured s3.bucket")
	}
	base := a.v.GetString("ducklake.data_path")
	if base == "" {
		base = "s3://" + bucket + "/ducklake/"
	}
	// A configured path is a root, never a shared per-namespace catalog path.
	dataPath := strings.TrimRight(base, "/") + "/namespaces/" + ns + "/"
	cfg := ducklake.Config{Binary: a.v.GetString("ducklake.binary"), CatalogConnection: conn, Namespace: ns, DataPath: dataPath, S3: objectstore.Config{
		Endpoint: a.v.GetString("s3.endpoint"), Region: a.v.GetString("s3.region"), Bucket: bucket, AccessKey: a.v.GetString("s3.access_key"), SecretKey: a.v.GetString("s3.secret_key"), SessionToken: a.v.GetString("s3.session_token"),
	}}
	return cfg, ducklake.Validate(cfg)
}
func (a *application) openPayloadStore(ctx context.Context, ns string) (*ducklake.PayloadStore, error) {
	cfg, err := a.runtimeLakeConfig(ns)
	if err != nil {
		return nil, err
	}
	root := a.v.GetString("ducklake.spool_dir")
	if root == "" {
		root = os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			root = filepath.Join(home, ".local", "state")
		}
		root = filepath.Join(root, "looster", "ducklake-spool")
	}
	// Keep spool identity independent of passwords and TLS rotation.
	id, err := ducklake.CatalogIdentity(cfg)
	if err != nil {
		return nil, err
	}
	spool := filepath.Join(root, id)
	lake, err := ducklake.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return ducklake.NewPayloadStore(ctx, lake, ducklake.PayloadOptions{SpoolDir: spool})
}
