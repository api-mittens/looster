package objectstore

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type transport func(*http.Request) (*http.Response, error)

func TestS3EnvironmentOnlyCredentials(t *testing.T) {
	for _, tc := range []struct {
		name          string
		config        Config
		env           map[string]string
		access, token string
		bad           bool
	}{
		{name: "AWS environment", env: map[string]string{"AWS_ACCESS_KEY_ID": "env-access", "AWS_SECRET_ACCESS_KEY": "env-secret", "AWS_SESSION_TOKEN": "env-token"}, access: "env-access", token: "env-token"},
		{name: "explicit LOOSTER wins", config: Config{AccessKey: "looster-access", SecretKey: "looster-secret", SessionToken: "looster-token"}, env: map[string]string{"AWS_ACCESS_KEY_ID": "other", "AWS_SECRET_ACCESS_KEY": "other", "AWS_SESSION_TOKEN": "other"}, access: "looster-access", token: "looster-token"},
		{name: "no shared credentials", bad: true},
		{name: "incomplete AWS", env: map[string]string{"AWS_ACCESS_KEY_ID": "do-not-print"}, bad: true},
		{name: "incomplete explicit does not mix", config: Config{AccessKey: "do-not-print"}, env: map[string]string{"AWS_ACCESS_KEY_ID": "other", "AWS_SECRET_ACCESS_KEY": "other"}, bad: true},
		{name: "token only does not mix", config: Config{SessionToken: "do-not-print"}, env: map[string]string{"AWS_ACCESS_KEY_ID": "other", "AWS_SECRET_ACCESS_KEY": "other"}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
				t.Setenv(key, "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			dir := t.TempDir()
			file := filepath.Join(dir, "credentials")
			if err := os.WriteFile(file, []byte("[polluted]\naws_access_key_id=file-access\naws_secret_access_key=file-secret\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", file)
			t.Setenv("AWS_CONFIG_FILE", file)
			t.Setenv("AWS_PROFILE", "polluted")
			tc.config.Bucket = "test"
			store, err := New(context.Background(), tc.config)
			if tc.bad {
				if err == nil {
					t.Fatal("file credentials or incomplete pair accepted")
				}
				if strings.Contains(err.Error(), "do-not-print") || strings.Contains(err.Error(), "file-secret") {
					t.Fatal("secret leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			creds, err := store.client.Options().Credentials.Retrieve(context.Background())
			if err != nil || creds.AccessKeyID != tc.access || creds.SessionToken != tc.token {
				t.Fatal("incorrect credential source or token")
			}
		})
	}
}

func (f transport) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestS3CompatibleRequests(t *testing.T) {
	ctx := context.Background()
	store, e := New(ctx, Config{Endpoint: "https://storage.example.test", Region: "eu-north-1", Bucket: "looster", AccessKey: "test-access", SecretKey: "test-secret", SessionToken: "test-token"})
	if e != nil {
		t.Fatal(e)
	}
	methods := []string{}
	opts := store.client.Options()
	opts.HTTPClient = transport(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		if r.URL.Host != "storage.example.test" || r.URL.Path != "/looster/tasks/TSK-1/spec.txt" {
			t.Errorf("incorrect S3 endpoint/path: %s", r.URL)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "test-access/") || !strings.Contains(r.Header.Get("Authorization"), "/eu-north-1/s3/aws4_request") {
			t.Error("missing configured credentials or signing region")
		}
		if r.Header.Get("X-Amz-Security-Token") != "test-token" {
			t.Error("missing session token signing header")
		}
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("history")), Request: r}, nil
	})
	store.client = s3.New(opts)
	if e = store.Put(ctx, "tasks/TSK-1/spec.txt", strings.NewReader("spec"), 4); e != nil {
		t.Fatal(e)
	}
	body, e := store.Get(ctx, "tasks/TSK-1/spec.txt")
	if e != nil {
		t.Fatal(e)
	}
	defer body.Close()
	b, e := io.ReadAll(body)
	if e != nil || string(b) != "history" {
		t.Fatalf("body=%s error=%v", b, e)
	}
	if e = store.Delete(ctx, "tasks/TSK-1/spec.txt"); e != nil {
		t.Fatal(e)
	}
	if strings.Join(methods, ",") != "PUT,GET,DELETE" {
		t.Fatalf("methods=%v", methods)
	}
}
