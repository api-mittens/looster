package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/namespace"
	"github.com/spf13/viper"
)

func TestNamespaceFlagEnvironmentPrecedence(t *testing.T) {
	t.Setenv("LOOSTER_NAMESPACE", "")
	a := &application{v: viper.New()}
	root := a.command()
	if got := a.v.GetString("namespace"); got != namespace.Default {
		t.Fatalf("default: %q", got)
	}
	t.Setenv("LOOSTER_NAMESPACE", "environment")
	if got := a.v.GetString("namespace"); got != "environment" {
		t.Fatalf("environment: %q", got)
	}
	if err := root.ParseFlags([]string{"-N", "selected"}); err != nil {
		t.Fatal(err)
	}
	if got := a.v.GetString("namespace"); got != "selected" {
		t.Fatalf("flag: %q", got)
	}
}

func TestNamespaceRejectsInvalidBeforeConnecting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOOSTER_DATABASE_URL", "invalid")
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"-N", "bad;sql", "task", "list"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "invalid namespace") {
		t.Fatalf("validation: %v", err)
	}
}

func TestNamespaceAdministrationRequiresHuman(t *testing.T) {
	for _, args := range [][]string{{"namespace", "create", "example"}, {"namespace", "list"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("LOOSTER_DATABASE_URL", "invalid")
			t.Setenv("LOOSTER_ROLE", "agent")
			t.Setenv("LOOSTER_AGENT", "coder")
			var out bytes.Buffer
			err := Execute(context.Background(), args, strings.NewReader(""), &out, &out)
			if !errors.Is(err, app.ErrForbidden) {
				t.Fatalf("expected authorization before DB, got %v", err)
			}
		})
	}
}

func TestTagCreateExplainsManifestBeforeDatabase(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOOSTER_DATABASE_URL", "invalid")
	t.Setenv("LOOSTER_ROLE", "human")
	var out bytes.Buffer
	err := Execute(context.Background(), []string{"tag", "create", "example"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "behaviour manifest") {
		t.Fatalf("expected manifest guidance before database setup: %v", err)
	}
}
