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
					if catalog != "ducklake" {
						t.Fatal(catalog)
					}
					return "resolved-connection", nil
				},
				validate: func(c ducklake.Config) error {
					calls = append(calls, "validate")
					if c.CatalogConnection != "resolved-connection" || c.DataPath != "s3://lake-bucket/prefix/" || c.S3.Bucket != "lake-bucket" {
						t.Fatal("bad lake config")
					}
					return nil
				},
				configure: func(_ context.Context, _, _, catalog, ns string) (string, error) {
					calls = append(calls, "configure")
					if catalog != "ducklake" || ns != "default" {
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
		{"namespace", "tenant", []string{"storage", "--ducklake"}},
		{"disabled flag", "default", []string{"storage", "--ducklake-database", "other"}},
		{"disabled path", "default", []string{"storage", "--ducklake-data-path", "s3://bucket/path/"}},
		{"disabled binary", "default", []string{"storage", "--duckdb-binary", "missing"}},
		{"empty catalog", "default", []string{"storage", "--ducklake", "--ducklake-database", ""}},
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

func TestConfigureStorageValidationFailureHasNoSideEffects(t *testing.T) {
	a := &application{v: viper.New()}
	a.v.Set("namespace", "default")
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
