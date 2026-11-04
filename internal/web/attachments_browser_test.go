package web

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/task"
	"github.com/chromedp/chromedp"
)

type browserAttachmentRepo struct {
	testRepo
	mu    sync.Mutex
	files []task.Attachment
}

func (r *browserAttachmentRepo) Attachments(context.Context, int64) ([]task.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task.Attachment(nil), r.files...), nil
}
func (r *browserAttachmentRepo) AddAttachment(_ context.Context, a task.Attachment) (task.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a.ID = int64(len(r.files) + 1)
	r.files = append(r.files, a)
	return a, nil
}
func (r *browserAttachmentRepo) Logs(_ context.Context, f event.Filter) ([]event.Event, error) {
	if len(f.Kinds) == 1 && f.Kinds[0] == "comment" && f.After < 42 {
		return []event.Event{{ID: 42, TaskID: &f.TaskID, Kind: "comment", Body: "Attachment comment", CreatedAt: time.Now()}}, nil
	}
	return []event.Event{}, nil
}

type browserAttachmentObjects struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *browserAttachmentObjects) Put(_ context.Context, k string, r io.Reader, _ int64) error {
	b, e := io.ReadAll(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[k] = b
	return e
}
func (s *browserAttachmentObjects) Get(_ context.Context, k string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.NopCloser(bytes.NewReader(s.data[k])), nil
}

// Uses the real embedded dashboard, multipart file picker, attachment routes and
// image decoding. Repository and private object storage are isolated test ports.
func TestAttachmentBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for real Chromium attachment acceptance")
	}
	repo := &browserAttachmentRepo{}
	objects := &browserAttachmentObjects{data: map[string][]byte{}}
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	// Seed the same metadata/key shape produced by task file add, without browser MIME metadata.
	objects.data["tasks/TSK-1/cli/legacy.png"] = img.Bytes()
	repo.files = []task.Attachment{{ID: 1, TaskID: 1, Name: "legacy.png", Key: "tasks/TSK-1/cli/legacy.png", Size: int64(img.Len())}}
	server := httptest.NewServer(HandlerWithStorage(repo, objects))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "upload.png")
	if err := os.WriteFile(file, img.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	wait := func(expression string) chromedp.Action {
		return chromedp.Poll(expression, nil, chromedp.WithPollingTimeout(20*time.Second))
	}
	if err := chromedp.Run(tab,
		chromedp.Navigate(server.URL+"/#board"),
		chromedp.Click("#toggle-stages"),
		chromedp.WaitVisible("task-card"),
		chromedp.Click("task-card"),
		chromedp.WaitVisible("#description-attachments img"),
		wait(`document.querySelector('#description-attachments img')?.naturalWidth === 3`),
		chromedp.SetUploadFiles(`#description-upload input[type=file]`, []string{file}),
		chromedp.Click("#description-upload button"),
		wait(`document.querySelectorAll('#description-attachments li').length === 2`),
		chromedp.SetUploadFiles(`[data-comment-id="42"] input[type=file]`, []string{file}),
		chromedp.Click(`[data-comment-id="42"] .attachment-upload button`),
		wait(`document.querySelector('[data-comment-id="42"] .comment-attachments img')?.naturalWidth === 3`),
		wait(`document.querySelectorAll('#description-attachments li').length === 2 && document.querySelectorAll('[data-comment-id="42"] .comment-attachments li').length === 1`),
	); err != nil {
		t.Fatal(err)
	}
	files, _ := repo.Attachments(ctx, 1)
	if len(files) != 3 || files[2].EventID == nil || *files[2].EventID != 42 {
		t.Fatalf("wrong persisted grouping: %+v", files)
	}
}
