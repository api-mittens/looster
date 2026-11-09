package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/app"
)

func TestAgentAdministrativeCommandsDeniedBeforeDatabase(t *testing.T) {
	for _, args := range [][]string{
		{"tag", "create", "design"},
		{"tag", "rm", "design"},
		{"agent", "create", "other"},
		{"agent", "rm", "other"},
		{"behaviour", "set"},
		{"behaviour", "create"},
		{"serve"},
		{"db", "migrate"},
		{"configure", "storage"},
		{"configure", "storage", "--ducklake"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("LOOSTER_DATABASE_URL", "invalid")
			t.Setenv("LOOSTER_ROLE", "agent")
			t.Setenv("LOOSTER_AGENT", "coder")
			var out bytes.Buffer
			err := Execute(context.Background(), args, strings.NewReader(""), &out, &out)
			if !errors.Is(err, app.ErrForbidden) {
				t.Fatalf("expected permission denial before connecting, got %v", err)
			}
		})
	}
}

func TestInvalidRoleFailsClosed(t *testing.T) {
	for _, args := range [][]string{{"--role", "admin", "status"}, {"--role", "agent", "--agent-name", "../coder", "status"}, {"--role", "agent", "status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("LOOSTER_DATABASE_URL", "invalid")
			t.Setenv("LOOSTER_AGENT", "")
			var out bytes.Buffer
			err := Execute(context.Background(), args, strings.NewReader(""), &out, &out)
			if err == nil || strings.Contains(err.Error(), "parse") {
				t.Fatalf("invalid role did not fail before DB config: %v", err)
			}
		})
	}
}
