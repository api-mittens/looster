package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/spf13/viper"
)

func TestConfigureStorageOptionality(t *testing.T) {
	a := &application{v: viper.New()}
	a.v.Set("namespace", "work")
	a.v.Set("database_url", "configured-connection")
	called := false
	cmd := a.configureCommandWith(storageActions{
		configure: func(_ context.Context, raw, name, catalog, ns string) (string, error) {
			called = true
			if raw != "configured-connection" || name != "custom" || catalog != "" || ns != "work" {
				t.Fatalf("unexpected provisioning arguments: %q %q %q %q", raw, name, catalog, ns)
			}
			return "selected", nil
		},
		// Nil lake actions ensure the PostgreSQL-only path cannot touch the engine,
		// catalog connection resolver, credentials or S3.
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"storage", "--database", "custom"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(out.String(), "not persisted") {
		t.Fatalf("missing provisioning/override guidance: %s", out.String())
	}
}

func TestConfigureStorageDuckLakeOrderAndPartialFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial"}[fail], func(t *testing.T) {
			a := &application{v: viper.New()}
			a.v.Set("namespace", "default")
			a.v.Set("s3.bucket", "lake-bucket")
			var calls []string
			actions := storageActions{
				connection: func(_, catalog string) (string, error) {
					calls = append(calls, "connection")
					if catalog != "" {
						t.Fatal(catalog)
					}
					return "resolved-connection", nil
				},
				validate: func(c ducklake.Config) error {
					calls = append(calls, "validate")
					if c.CatalogConnection != "resolved-connection" || c.DataPath != "s3://lake-bucket/prefix/namespaces/default/" || c.S3.Bucket != "lake-bucket" {
						t.Fatal("bad lake config")
					}
					return nil
				},
				configure: func(_ context.Context, _, _, catalog, ns string) (string, error) {
					calls = append(calls, "configure")
					if catalog != "" || ns != "default" {
						t.Fatal("bad provisioning settings")
					}
					return "", nil
				},
				attach: func(context.Context, ducklake.Config) error {
					calls = append(calls, "attach")
					if fail {
						return errors.New("sanitized engine failure")
					}
					return nil
				},
			}
			cmd := a.configureCommandWith(actions)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"storage", "--ducklake", "--ducklake-data-path", "s3://lake-bucket/prefix/"})
			err := cmd.Execute()
			if fail {
				if err == nil || !strings.Contains(err.Error(), "application migrations completed and databases retained") {
					t.Fatalf("partial failure: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, []string{"connection", "validate", "configure", "attach"}) {
				t.Fatal(calls)
			}
		})
	}
}

func TestConfigureStorageRejectsUnsafeOptInBeforeProvisioning(t *testing.T) {
	for _, tc := range []struct {
		name, ns string
		args     []string
	}{
		{"namespace", "INVALID", []string{"storage", "--ducklake"}},
		{"disabled flag", "default", []string{"storage", "--ducklake-database", "other"}},
		{"disabled path", "default", []string{"storage", "--ducklake-data-path", "s3://bucket/path/"}},
		{"disabled binary", "default", []string{"storage", "--duckdb-binary", "missing"}},
		{"empty catalog", "default", []string{"storage", "--ducklake", "--ducklake-database", ""}},
		{"empty path", "default", []string{"storage", "--ducklake", "--ducklake-data-path="}},
		{"explicit false", "default", []string{"storage", "--ducklake=false", "--ducklake-data-path", "s3://bucket/path/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &application{v: viper.New()}
			a.v.Set("namespace", tc.ns)
			cmd := a.configureCommandWith(storageActions{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatal("expected validation before nil backend called")
			}
		})
	}
}

func TestConfigureStorageDuckLakeResolvedPath(t *testing.T) {
	for _, tc := range []struct {
		name, bucket, path string
		args               []string
	}{
		{"configured default", "configured-bucket", "s3://configured-bucket/ducklake/", nil},
		{"environment default", "", "s3://environment-bucket/ducklake/", nil},
		{"dotted bucket", "my.lake-bucket", "s3://my.lake-bucket/ducklake/", nil},
		{"override", "configured-bucket", "s3://configured-bucket/custom/prefix/", []string{"--ducklake-data-path", "s3://configured-bucket/custom/prefix/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOSTER_S3_BUCKET", "environment-bucket")
			v := viper.New()
			if err := v.BindEnv("s3.bucket", "LOOSTER_S3_BUCKET"); err != nil {
				t.Fatal(err)
			}
			v.Set("namespace", "default")
			if tc.bucket != "" {
				v.Set("s3.bucket", tc.bucket)
			}
			a := &application{v: v}
			var calls []string
			check := func(c ducklake.Config) {
				t.Helper()
				if c.DataPath != tc.path+"namespaces/default/" || c.S3.Bucket != v.GetString("s3.bucket") {
					t.Fatalf("unexpected data path %q or bucket %q", c.DataPath, c.S3.Bucket)
				}
			}
			cmd := a.configureCommandWith(storageActions{
				connection: func(string, string) (string, error) {
					calls = append(calls, "connection")
					return "resolved", nil
				},
				validate: func(c ducklake.Config) error {
					calls = append(calls, "validate")
					check(c)
					return nil
				},
				configure: func(context.Context, string, string, string, string) (string, error) {
					calls = append(calls, "configure")
					return "", nil
				},
				attach: func(_ context.Context, c ducklake.Config) error {
					calls = append(calls, "attach")
					check(c)
					return nil
				},
			})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(append([]string{"storage", "--ducklake"}, tc.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, []string{"connection", "validate", "configure", "attach"}) {
				t.Fatal(calls)
			}
		})
	}
}

func TestConfigureStorageDuckLakeInvalidDefaultBucket(t *testing.T) {
	for _, bucket := range []string{"", " ", "ab", "UPPER-bucket", "bucket/prefix", "s3://bucket", "bucket:9000", "bucket?query", "bucket#fragment", "user@bucket", "bucket..name", "-bucket", "bucket-", "bucket.-name", "127.0.0.1", strings.Repeat("a", 64)} {
		t.Run(bucket, func(t *testing.T) {
			a := &application{v: viper.New()}
			a.v.Set("namespace", "default")
			a.v.Set("s3.bucket", bucket)
			// All actions are nil, so even connection resolution must not run.
			cmd := a.configureCommandWith(storageActions{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"storage", "--ducklake"})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "valid configured s3.bucket") {
				t.Fatalf("expected bucket validation error, got %v", err)
			}
		})
	}
}

func TestConfigureStorageHelpDocumentsDuckLakeDefault(t *testing.T) {
	a := &application{v: viper.New()}
	cmd := a.configureCommandWith(storageActions{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"storage", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "s3://<configured s3.bucket>/ducklake/") || strings.Contains(out.String(), "required with --ducklake") {
		t.Fatalf("unexpected help: %s", out.String())
	}
	for _, example := range []string{"looster configure storage --ducklake\n", "--ducklake-data-path s3://my-bucket/custom-prefix/"} {
		if !strings.Contains(out.String(), example) {
			t.Fatalf("missing example %q in help: %s", example, out.String())
		}
	}
}

func TestConfigureStorageValidationFailureHasNoSideEffects(t *testing.T) {
	a := &application{v: viper.New()}
	a.v.Set("namespace", "default")
	a.v.Set("s3.bucket", "lake-bucket")
	cmd := a.configureCommandWith(storageActions{
		connection: func(string, string) (string, error) { return "resolved", nil },
		validate:   func(ducklake.Config) error { return errors.New("invalid lake settings") },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"storage", "--ducklake"})
	if err := cmd.Execute(); err == nil || err.Error() != "invalid lake settings" {
		t.Fatalf("validation: %v", err)
	}
}

func TestConfigureStorageCatalogFailureHasNoSideEffects(t *testing.T) {
	a := &application{v: viper.New()}
	a.v.Set("namespace", "default")
	a.v.Set("s3.bucket", "lake-bucket")
	want := errors.New("catalog must differ from application database")
	cmd := a.configureCommandWith(storageActions{
		connection: func(string, string) (string, error) { return "", want },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"storage", "--ducklake"})
	if err := cmd.Execute(); !errors.Is(err, want) {
		t.Fatalf("catalog safeguard: %v", err)
	}
}
