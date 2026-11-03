package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dominikbb/looster"
)

func TestLicensesOffline(t *testing.T) {
	t.Setenv("LOOSTER_DATABASE_URL", "invalid database")
	t.Setenv("LOOSTER_ROLE", "invalid role")
	var output, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"--config", "/does/not/exist.toml", "licenses"}, strings.NewReader(""), &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), looster.License+"\n"+looster.ThirdPartyNotices; got != want {
		t.Fatal("license command omitted or changed embedded notice texts")
	}
	for _, text := range []string{"MIT License", "github.com/hashicorp/hcl", "Mozilla Public License", "https://proxy.golang.org/github.com/hashicorp/hcl/@v/v1.0.0.zip", "D3"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("missing license/source attribution %q", text)
		}
	}
}

type failingLicenseWriter struct{}

func (failingLicenseWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLicensesWriteError(t *testing.T) {
	err := Execute(context.Background(), []string{"licenses"}, strings.NewReader(""), failingLicenseWriter{}, io.Discard)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error = %v", err)
	}
}
