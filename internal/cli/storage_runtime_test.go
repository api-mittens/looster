package cli

import (
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/spf13/viper"
)

func runtimeApplication(t *testing.T) *application {
	t.Helper()
	a := &application{v: viper.New()}
	a.v.Set("database_url", "postgres://user:secret@localhost:5432/existing?sslmode=disable")
	a.v.Set("s3.bucket", "test-bucket")
	a.v.Set("s3.access_key", "access")
	a.v.Set("s3.secret_key", "secret")
	return a
}
func TestRuntimeLakeConfigCredentialsOnly(t *testing.T) {
	a := runtimeApplication(t)
	a.v.Set("database_url", a.v.GetString("database_url")+"&pool_max_conns=14")
	c, err := a.runtimeLakeConfig("default")
	if err != nil {
		t.Fatal(err)
	}
	if c.DataPath != "s3://test-bucket/ducklake/namespaces/default/" || !strings.Contains(c.CatalogConnection, "dbname='existing'") {
		t.Fatalf("unexpected derived config path %q", c.DataPath)
	}
	if strings.Contains(c.CatalogConnection, "pool_max_conns") || !strings.Contains(a.v.GetString("database_url"), "pool_max_conns=14") {
		t.Fatal("runtime must filter native pool settings without changing application configuration")
	}
	other, err := a.runtimeLakeConfig("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if other.DataPath == c.DataPath || other.Namespace == c.Namespace {
		t.Fatal("namespace shared data prefix")
	}
	id, err := ducklake.CatalogIdentity(c)
	if err != nil {
		t.Fatal(err)
	}
	c.CatalogConnection = strings.ReplaceAll(c.CatalogConnection, "secret", "rotated")
	rotated, err := ducklake.CatalogIdentity(c)
	if err != nil || id != rotated {
		t.Fatal("credential rotation stranded spool")
	}
}
func TestRuntimeLakeConfigOverrides(t *testing.T) {
	a := runtimeApplication(t)
	a.v.Set("ducklake.database", "legacy_catalog")
	a.v.Set("ducklake.data_path", "s3://test-bucket/custom/root/")
	a.v.Set("ducklake.binary", "/must-not-execute/duckdb") // Obsolete settings cannot select an external engine.
	c, err := a.runtimeLakeConfig("tenant")
	if err != nil {
		t.Fatal(err)
	}
	if c.DataPath != "s3://test-bucket/custom/root/namespaces/tenant/" || !strings.Contains(c.CatalogConnection, "dbname='legacy_catalog'") {
		t.Fatal("overrides not retained")
	}
}
func TestRuntimeLakeConfigFailClosed(t *testing.T) {
	for _, key := range []string{"s3.bucket", "s3.access_key", "s3.secret_key"} {
		t.Run(key, func(t *testing.T) {
			a := runtimeApplication(t)
			a.v.Set(key, "")
			if _, err := a.runtimeLakeConfig("default"); err == nil {
				t.Fatal("missing mandatory credential accepted")
			}
		})
	}
	a := runtimeApplication(t)
	if _, err := a.runtimeLakeConfig("../escape"); err == nil {
		t.Fatal("invalid namespace accepted")
	}
}
