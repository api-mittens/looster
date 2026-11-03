package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
	"github.com/api-mittens/looster/internal/web"
	"github.com/spf13/viper"
)

type cliAttachmentRepo struct {
	app.Repository
	files []task.Attachment
}

func (r *cliAttachmentRepo) Task(context.Context, int64) (task.Task, error) {
	return task.Task{ID: 1}, nil
}
func (r *cliAttachmentRepo) AddAttachment(_ context.Context, a task.Attachment) (task.Attachment, error) {
	a.ID = int64(len(r.files) + 1)
	r.files = append(r.files, a)
	return a, nil
}
func (r *cliAttachmentRepo) Attachments(context.Context, int64) ([]task.Attachment, error) {
	return r.files, nil
}

// Runs the existing CLI file command through the real signed S3 HTTP adapter,
// then reads those same records and objects through the dashboard attachment API.
func TestCLIImagesVisibleInDashboard(t *testing.T) {
	for _, ns := range []string{"default", "pictures"} {
		t.Run(ns, func(t *testing.T) {
			var mu sync.Mutex
			data := map[string][]byte{}
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case "PUT":
					b, e := io.ReadAll(r.Body)
					if e != nil {
						t.Error(e)
					}
					data[r.URL.Path] = b
				case "GET":
					b, ok := data[r.URL.Path]
					if !ok {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write(b)
				default:
					http.Error(w, "unexpected method", 405)
				}
			}))
			defer storage.Close()
			var img bytes.Buffer
			if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "from-cli.png")
			if err := os.WriteFile(file, img.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			repo := &cliAttachmentRepo{}
			a := &application{v: viper.New(), repo: repo}
			for k, v := range map[string]string{"namespace": ns, "s3.endpoint": storage.URL, "s3.bucket": "test", "s3.region": "us-east-1", "s3.access_key": "fixture-access", "s3.secret_key": "fixture-secret"} {
				a.v.Set(k, v)
			}
			for _, args := range [][]string{{"add", "TSK-1", file}, {"add", "TSK-1", file, "--comment", "42"}} {
				cmd := a.fileCommand()
				cmd.SetArgs(args)
				cmd.SetOut(io.Discard)
				if err := cmd.ExecuteContext(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			objects, err := a.objects(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			provider := &webNamespaces{selected: ns, repos: map[string]app.Repository{ns: repo}}
			handler := web.HandlerWithStorage(repo, objects, web.WithNamespaces(provider))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/attachments", nil))
			var files []struct {
				ID          int64  `json:"id"`
				EventID     *int64 `json:"event_id"`
				Previewable bool   `json:"previewable"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &files) != nil || len(files) != 2 || files[0].EventID != nil || files[1].EventID == nil || *files[1].EventID != 42 || !files[0].Previewable {
				t.Fatalf("CLI metadata not visible: %d %s", response.Code, response.Body)
			}
			if strings.Contains(response.Body.String(), `"key"`) {
				t.Fatal("object key exposed")
			}
			for _, route := range []string{"preview", "download"} {
				response = httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/attachments/1/"+route, nil))
				if response.Code != 200 {
					t.Fatalf("CLI image %s: %d %s", route, response.Code, response.Body)
				}
				if route == "preview" {
					decoded, _, err := image.Decode(response.Body)
					if err != nil || decoded.Bounds().Dy() != 3 {
						t.Fatalf("invalid preview: %v", err)
					}
				} else if !bytes.Equal(response.Body.Bytes(), img.Bytes()) {
					t.Fatal("download changed CLI bytes")
				}
			}
		})
	}
}
