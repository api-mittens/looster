package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/jcode"
	"github.com/spf13/viper"
)

func TestConfigRejectsSecretsEvenWithEnvironmentOverride(t *testing.T) {
	isolateConfigEnvironment(t)
	const secret = "DO-NOT-PRINT-file-secret"
	for _, key := range []string{"database_url", "s3.access_key", "s3.secret_key", "s3.session_token"} {
		for _, value := range []string{"'" + secret + "'", "''", "123", "['" + secret + "']"} {
			t.Run(key+value, func(t *testing.T) {
				t.Setenv("LOOSTER_"+strings.ToUpper(strings.ReplaceAll(key, ".", "_")), "DO-NOT-PRINT-env-secret")
				path := filepath.Join(t.TempDir(), "config.toml")
				if err := os.WriteFile(path, []byte(key+"="+value+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				err := Execute(context.Background(), []string{"--config", path, "behaviour", "check"}, strings.NewReader("ON TODO PING coder\n"), &out, &out)
				want := "configuration key " + key + " is environment-only; remove it from TOML"
				if err == nil || err.Error() != want {
					t.Fatalf("expected key-only rejection for %s", key)
				}
				if strings.Contains(out.String(), secret) || strings.Contains(out.String(), "DO-NOT-PRINT-env-secret") {
					t.Fatal("secret leaked in output")
				}
			})
		}
	}
}

func TestMalformedConfigDoesNotLeakValues(t *testing.T) {
	isolateConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("database_url = DO-NOT-PRINT-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &application{v: viper.New()}
	a.command()
	a.config = path
	err := a.load()
	if err == nil || err.Error() != "invalid TOML configuration (values omitted)" {
		t.Fatal("expected sanitized TOML parse error")
	}
}

func TestWorkerEnvironmentPreservesSelectedConfigAndEnvSecrets(t *testing.T) {
	isolateConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[s3]\nendpoint='http://selected:9000'\nbucket='selected'\nregion='selected-region'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"LOOSTER_S3_ENDPOINT": "http://selected:9000", "LOOSTER_S3_BUCKET": "selected", "LOOSTER_S3_REGION": "selected-region", "LOOSTER_S3_ACCESS_KEY": "env-access", "LOOSTER_S3_SECRET_KEY": "env-secret", "LOOSTER_S3_SESSION_TOKEN": "env-token"}
	for _, key := range []string{"LOOSTER_S3_ACCESS_KEY", "LOOSTER_S3_SECRET_KEY", "LOOSTER_S3_SESSION_TOKEN"} {
		t.Setenv(key, expected[key])
	}
	a := &application{v: viper.New()}
	a.command()
	a.config = path
	if err := a.load(); err != nil {
		t.Fatal(err)
	}
	got := a.workerEnvironment()
	for key, want := range expected {
		if got[key] != want {
			t.Fatalf("worker environment lost %s", key)
		}
	}
	// Exercise the real harness boundary, including PG* fallback credentials.
	dir := t.TempDir()
	binary := filepath.Join(dir, "jcode")
	script := `#!/bin/sh
[ "$LOOSTER_S3_ENDPOINT" = 'http://selected:9000' ] || exit 11
[ "$LOOSTER_S3_BUCKET" = selected ] || exit 12
[ "$LOOSTER_S3_REGION" = selected-region ] || exit 13
[ "$LOOSTER_S3_ACCESS_KEY" = env-access ] || exit 14
[ "$LOOSTER_S3_SECRET_KEY" = env-secret ] || exit 15
[ "$LOOSTER_S3_SESSION_TOKEN" = env-token ] || exit 16
[ "$PGPASSWORD" = pg-env-password ] || exit 17
[ -z "$LOOSTER_DATABASE_URL" ] || exit 18
printf '%s\n' '{"type":"done","session_id":"env_test"}'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env_test.json"), []byte(`{"messages":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSWORD", "pg-env-password")
	// Deliberately pollute the shell after the selected env was constructed.
	t.Setenv("LOOSTER_S3_BUCKET", "unselected-bucket")
	h := jcode.Harness{Binary: binary, SessionsDir: dir, Environment: got}
	if _, err := h.Run(context.Background(), app.Run{Agent: "coder", Prompt: "test"}, func(app.Output) error { return nil }); err != nil {
		t.Fatalf("worker lost selected environment: %v", err)
	}

	// Clearing a credential must not resurrect any old shell or config value.
	t.Setenv("LOOSTER_S3_SESSION_TOKEN", "")
	if a.workerEnvironment()["LOOSTER_S3_SESSION_TOKEN"] != "" {
		t.Fatal("stale session token exported")
	}
}

func TestDatabaseURLFlagRemoved(t *testing.T) {
	isolateConfigEnvironment(t)
	for _, args := range [][]string{{"--database-url", "DO-NOT-PRINT-secret", "status"}, {"--database-url=DO-NOT-PRINT-secret", "status"}} {
		var out bytes.Buffer
		err := Execute(context.Background(), args, strings.NewReader(""), &out, &out)
		if err == nil || !strings.Contains(err.Error(), "unknown flag: --database-url") {
			t.Fatal("database URL flag was accepted")
		}
		if strings.Contains(err.Error()+out.String(), "DO-NOT-PRINT-secret") {
			t.Fatal("flag value leaked")
		}
	}
}
