package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestSecretConfigKeySpellings(t *testing.T) {
	isolateConfigEnvironment(t)
	for _, tc := range []struct{ body, key string }{
		{"[s3]\naccess_key='private-value'", "s3.access_key"},
		{"[S3]\nSECRET_KEY='private-value'", "s3.secret_key"},
		{"s3 = {session_token='private-value'}", "s3.session_token"},
		{"\"database_url\"='private-value'", "database_url"},
		{"s3.\"secret_key\"='private-value'", "s3.secret_key"},
		{"\"s3.secret_key\"='private-value'", "s3.secret_key"},
	} {
		t.Run(tc.key+tc.body[:2], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			a := &application{v: viper.New()}
			a.command()
			a.config = path
			err := a.load()
			if err == nil || !strings.Contains(err.Error(), "configuration key "+tc.key+" is environment-only") || strings.Contains(err.Error(), "private-value") {
				t.Fatal("secret key spelling bypassed safe rejection")
			}
		})
	}
}
