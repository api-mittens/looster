package web

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/task"
)

type attachmentTestRepo struct {
	testRepo
	files  []task.Attachment
	addErr error
}

func (r *attachmentTestRepo) Attachments(context.Context, int64) ([]task.Attachment, error) {
	return r.files, nil
}
func (r *attachmentTestRepo) AddAttachment(_ context.Context, a task.Attachment) (task.Attachment, error) {
	if r.addErr != nil {
		return a, r.addErr
	}
	a.ID = 7
	r.files = append(r.files, a)
	return a, nil
}

type attachmentTestObjects struct {
	data    map[string][]byte
	deleted int
	gets    int
	putErr  error
}

func (s *attachmentTestObjects) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, _ := io.ReadAll(r)
	s.data[key] = b
	return s.putErr
}
func (s *attachmentTestObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.gets++
	return io.NopCloser(bytes.NewReader(s.data[key])), nil
}
func (s *attachmentTestObjects) Delete(_ context.Context, key string) error {
	s.deleted++
	delete(s.data, key)
	return nil
}
func attachmentUpload(data []byte) *http.Request {
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	f, _ := m.CreateFormFile("file", "test.png")
	_, _ = f.Write(data)
	_ = m.Close()
	r := httptest.NewRequest("POST", "http://example.com/api/tasks/TSK-1/attachments", &b)
	r.Header.Set("Content-Type", m.FormDataContentType())
	return r
}
func attachmentRequest(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}
func TestAttachmentUploadListDownload(t *testing.T) {
	repo := &attachmentTestRepo{}
	objects := &attachmentTestObjects{data: map[string][]byte{}}
	h := HandlerWithStorage(repo, objects)
	r := attachmentUpload([]byte("<html>unsafe</html>"))
	r.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || strings.Contains(w.Body.String(), "key") {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	w = attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":7`) || strings.Contains(w.Body.String(), "dashboard/") {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments/7/download")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: %d %v", w.Code, w.Header())
	}
	if w = attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments/7/preview"); w.Code != 415 {
		t.Fatalf("HTML preview %d", w.Code)
	}
}
func TestAttachmentPreviews(t *testing.T) {
	var pngData bytes.Buffer
	_ = png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	var huge bytes.Buffer
	_ = png.Encode(&huge, image.NewGray(image.Rect(0, 0, maxImageDimension+1, 1)))
	for _, tc := range []struct {
		name string
		data []byte
		code int
	}{
		{"png", pngData.Bytes(), 200}, {"truncated", pngData.Bytes()[:40], 415}, {"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 415}, {"html", []byte("<html/>"), 415}, {"dimensions", huge.Bytes(), 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &attachmentTestRepo{files: []task.Attachment{{ID: 7, TaskID: 1, Key: "key", Name: "test.png"}}}
			objects := &attachmentTestObjects{data: map[string][]byte{"key": tc.data}}
			w := attachmentRequest(HandlerWithStorage(repo, objects), "GET", "/api/tasks/TSK-1/attachments/7/preview")
			if w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.code == 200 {
				if _, err := png.Decode(w.Body); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestAttachmentScopeAndUnavailable(t *testing.T) {
	repo := &attachmentTestRepo{files: []task.Attachment{{ID: 7, TaskID: 2, Key: "secret"}}}
	objects := &attachmentTestObjects{data: map[string][]byte{}}
	h := HandlerWithStorage(repo, objects)
	for _, target := range []string{"/api/tasks/TSK-1/attachments/7/download", "/api/tasks/TSK-2/attachments/7/download", "/api/tasks/TSK-1/attachments/7/preview"} {
		if w := attachmentRequest(h, "GET", target); w.Code != 404 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	if objects.gets != 0 {
		t.Fatal("read object outside task")
	}
	if w := attachmentRequest(Handler(repo), "GET", "/api/tasks/TSK-1/attachments"); w.Code != 503 {
		t.Fatalf("storage %d", w.Code)
	}
	if w := attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments?namespace=other"); w.Code != 400 {
		t.Fatalf("namespace %d", w.Code)
	}
}
func TestAttachmentWriteProtectionAndLimits(t *testing.T) {
	for _, tc := range []struct {
		origin, site string
		code         int
	}{{"https://evil.example", "", 403}, {"null", "", 403}, {"http://example.com", "same-site", 403}, {"", "cross-site", 403}, {"http://example.com", "same-origin", 201}} {
		repo := &attachmentTestRepo{}
		objects := &attachmentTestObjects{data: map[string][]byte{}}
		r := attachmentUpload([]byte("ok"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		HandlerWithStorage(repo, objects).ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
	for _, size := range []int{maxAttachmentBytes, maxAttachmentBytes + 1, maxAttachmentBytes + (128 << 10)} {
		repo := &attachmentTestRepo{}
		objects := &attachmentTestObjects{data: map[string][]byte{}}
		w := httptest.NewRecorder()
		HandlerWithStorage(repo, objects).ServeHTTP(w, attachmentUpload(make([]byte, size)))
		want := 201
		if size > maxAttachmentBytes {
			want = 413
		}
		if w.Code != want {
			t.Fatalf("size %d: %d %s", size, w.Code, w.Body)
		}
	}
}
func TestAttachmentPartialFailureCleanup(t *testing.T) {
	for _, putFails := range []bool{false, true} {
		repo := &attachmentTestRepo{addErr: errors.New("private DB detail")}
		objects := &attachmentTestObjects{data: map[string][]byte{}}
		if putFails {
			objects.putErr = errors.New("private bucket detail")
		}
		w := httptest.NewRecorder()
		HandlerWithStorage(repo, objects).ServeHTTP(w, attachmentUpload([]byte("data")))
		if w.Code != 500 || objects.deleted != 1 || len(objects.data) != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("cleanup %d %+v %s", w.Code, objects, w.Body)
		}
	}
}

func TestAttachmentNamespaceIsolation(t *testing.T) {
	base := &attachmentTestRepo{}
	alpha := &attachmentTestRepo{files: []task.Attachment{{ID: 7, TaskID: 1, Key: "alpha", Name: "alpha.txt"}}}
	beta := &attachmentTestRepo{files: []task.Attachment{{ID: 7, TaskID: 1, Key: "beta", Name: "beta.txt"}}}
	objects := &attachmentTestObjects{data: map[string][]byte{"alpha": []byte("alpha secret"), "beta": []byte("beta secret")}}
	h := HandlerWithStorage(base, objects, testNamespaces{map[string]Repository{"alpha": alpha, "beta": beta}})
	for _, namespace := range []string{"alpha", "beta"} {
		w := attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments/7/download?namespace="+namespace)
		if w.Code != 200 || w.Body.String() != namespace+" secret" {
			t.Fatalf("namespace %s: %d %s", namespace, w.Code, w.Body)
		}
	}
	r := attachmentUpload([]byte("uploaded"))
	r.URL.RawQuery = "namespace=alpha"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || len(alpha.files) != 2 || len(beta.files) != 1 || len(base.files) != 0 {
		t.Fatalf("upload escaped selected namespace: %d", w.Code)
	}
	for _, suffix := range []string{"", "?namespace=missing"} {
		w := attachmentRequest(h, "GET", "/api/tasks/TSK-1/attachments/7/download"+suffix)
		if w.Code != 404 {
			t.Fatalf("namespace fallback exposed attachment: %d", w.Code)
		}
	}
}
