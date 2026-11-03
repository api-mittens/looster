package objectstore

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestS3CompatibleRequests(t *testing.T) {
	ctx := context.Background()
	store, e := New(ctx, Config{Endpoint: "https://storage.example.test", Region: "eu-north-1", Bucket: "looster", AccessKey: "test-access", SecretKey: "test-secret"})
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
	if strings.Join(methods, ",") != "PUT,GET" {
		t.Fatalf("methods=%v", methods)
	}
}
