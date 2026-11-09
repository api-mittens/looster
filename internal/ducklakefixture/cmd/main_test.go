package main

import (
	"strings"
	"testing"
)

func TestCommandEnvironmentExcludesAmbientServices(t *testing.T) {
	for _, key := range []string{"PGHOST", "PGPASSWORD", "PGSERVICE", "AWS_ACCESS_KEY_ID", "AWS_PROFILE", "LOOSTER_DATABASE_URL", "LOOSTER_TEST_PROVISION_DATABASE_URL", "LOOSTER_TEST_S3_ENDPOINT"} {
		t.Setenv(key, "must-not-forward")
	}
	t.Setenv("TMPDIR", "must-not-forward")
	env := commandEnvironment("/disposable/scratch")
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "AWS") || strings.HasPrefix(key, "LOOSTER") || value == "must-not-forward" {
			t.Fatalf("ambient service configuration forwarded: %s", key)
		}
		if _, ok := values[key]; ok {
			t.Fatalf("duplicate key %s", key)
		}
		values[key] = value
	}
	if values["TMPDIR"] != "/disposable/scratch" || values["LC_ALL"] != "C" {
		t.Fatal("scratch or locale not pinned")
	}
}

func TestRunRequiresScratch(t *testing.T) {
	t.Setenv("JCODE_SCRATCH_DIR", "")
	if err := run(); err == nil || !strings.Contains(err.Error(), "JCODE_SCRATCH_DIR") {
		t.Fatalf("missing scratch accepted: %v", err)
	}
}

func TestRunRejectsMissingPostgresTools(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("JCODE_SCRATCH_DIR", scratch)
	t.Setenv("LOOSTER_TEST_PG_BIN", scratch+"/missing")
	if err := run(); err == nil {
		t.Fatal("missing PostgreSQL binaries accepted")
	}
}
