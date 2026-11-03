package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsGenerateOffline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent skills")
	var out bytes.Buffer
	// Neither the missing config nor the unusable DSN should affect generation.
	t.Setenv("LOOSTER_DATABASE_URL", "invalid-database-url")
	err := Execute(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "missing.toml"), "skills", "generate", dir}, strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Skill string `json:"skill"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Skill != "looster" || result.Path != filepath.Join(dir, "looster", "SKILL.md") {
		t.Fatalf("unexpected result %+v", result)
	}
	if data, err := os.ReadFile(result.Path); err != nil || len(data) == 0 {
		t.Fatalf("skill not written: %v", err)
	}
}
