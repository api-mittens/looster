package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func isolateConfigEnvironment(t *testing.T) {
	t.Helper()
	// Configuration precedence tests must not inherit a developer's active
	// Looster connection, namespace, or S3 settings. Viper ignores empty values.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LOOSTER_") {
			t.Setenv(key, "")
		}
	}
}

func TestConfigEnvironmentOverrides(t *testing.T) {
	isolateConfigEnvironment(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if e := os.WriteFile(path, []byte("database_url='postgres://file/db'\n[s3]\nendpoint='http://file:9000'\nbucket='file'\nregion='file'\naccess_key='file'\nsecret_key='file'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ key, env, value string }{
		{"database_url", "LOOSTER_DATABASE_URL", "postgres://env/db"},
		{"s3.endpoint", "LOOSTER_S3_ENDPOINT", "http://env:9000"},
		{"s3.bucket", "LOOSTER_S3_BUCKET", "env-bucket"},
		{"s3.region", "LOOSTER_S3_REGION", "eu-north-1"},
		{"s3.access_key", "LOOSTER_S3_ACCESS_KEY", "env-access"},
		{"s3.secret_key", "LOOSTER_S3_SECRET_KEY", "env-secret"},
		{"role", "LOOSTER_ROLE", "agent"},
		{"agent", "LOOSTER_AGENT", "coder"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)
			a := &application{v: viper.New(), config: path}
			a.command()
			a.config = path
			if e := a.load(); e != nil {
				t.Fatal(e)
			}
			if got := a.v.GetString(tc.key); got != tc.value {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestConfigFileAndFlagPrecedence(t *testing.T) {
	isolateConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if e := os.WriteFile(path, []byte("database_url='postgres://file/db'\n[s3]\nbucket='file-bucket'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	a := &application{v: viper.New()}
	root := a.command()
	a.config = path
	if e := a.load(); e != nil {
		t.Fatal(e)
	}
	if a.v.GetString("database_url") != "postgres://file/db" || a.v.GetString("s3.bucket") != "file-bucket" {
		t.Fatal("file settings not loaded")
	}
	t.Setenv("LOOSTER_DATABASE_URL", "postgres://env/db")
	if e := root.PersistentFlags().Set("database-url", "postgres://flag/db"); e != nil {
		t.Fatal(e)
	}
	if a.v.GetString("database_url") != "postgres://flag/db" {
		t.Fatal("flag did not override environment")
	}
}
func TestLocalCLI(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		in, want string
		bad      bool
	}{
		{"help", []string{"--help"}, "", "serve", false},
		{"DSL check", []string{"behaviour", "check"}, "ON TODO PING coder\n", "\"target\": \"coder\"", false},
		{"recurring DSL check", []string{"behaviour", "check"}, "EVERY 10 MINUTES PING AGENT_3 WITH \"Prompt ....\"\n", "\"interval_seconds\": 600", false},
		{"DSL invalid", []string{"behaviour", "check"}, "ON TODO START coder", "", true},
		{"arity", []string{"task", "create"}, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var out bytes.Buffer
			e := Execute(context.Background(), tc.args, strings.NewReader(tc.in), &out, &out)
			if (e != nil) != tc.bad {
				t.Fatalf("error %v, output %s", e, &out)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output %s", &out)
			}
		})
	}
}
