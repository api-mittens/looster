package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionWithoutDatabase(t *testing.T) {
	t.Setenv("LOOSTER_DATABASE_URL", "not a database URL")
	var out, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"--version"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out.String()), "looster version "+Version; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
