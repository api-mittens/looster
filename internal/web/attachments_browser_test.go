//go:build uitest

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/task"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type browserAttachmentRepo struct {
	testRepo
	mu    sync.Mutex
	files []task.Attachment
}

func TestAttachmentResponsiveRefreshBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for real Chromium attachment acceptance")
	}
	owner := int64(42)
	name := strings.Repeat("long-unbroken-filename", 14) + ".png"
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 1600, 900))); err != nil {
		t.Fatal(err)
	}
	repo := &browserAttachmentRepo{files: []task.Attachment{
		{ID: 1, TaskID: 1, Name: name, Key: "tasks/TSK-1/cli/wide.png", Size: int64(img.Len())},
		{ID: 2, TaskID: 1, EventID: &owner, Name: name, Key: "tasks/TSK-1/cli/wide.png", Size: int64(img.Len())},
	}}
	objects := &browserAttachmentObjects{data: map[string][]byte{"tasks/TSK-1/cli/wide.png": img.Bytes()}}
	server := httptest.NewServer(HandlerWithStorage(repo, objects))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.Click("#toggle-stages"), chromedp.WaitVisible("task-card"), chromedp.Click("task-card"), chromedp.WaitVisible(".comment-attachments img"), chromedp.Poll(`document.querySelector('#description-attachments img')?.naturalWidth === 1600`, nil)); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int64{1440, 375, 280} {
		var valid bool
		err := chromedp.Run(tab, chromedp.EmulateViewport(width, 900), chromedp.Evaluate(`(() => {
		  const lists=[...document.querySelectorAll('.attachments')];
		  return lists.length===2 && lists.every(list => [...list.children].every(tile => {
		    const r=tile.getBoundingClientRect(), p=list.getBoundingClientRect(), image=tile.querySelector('img'), i=image.getBoundingClientRect();
		    return r.width<=241 && r.left>=p.left-1 && r.right<=p.right+1 && tile.scrollWidth<=tile.clientWidth+1 && i.width<=r.width && i.height<=161 && getComputedStyle(image).objectFit==='contain';
		  })) && document.querySelector('#description-attachments [data-refresh-key="attachment:1"]') && !!document.querySelector('[data-comment-id="42"] [data-refresh-key="attachment:2"]');
		})()`, &valid))
		if err != nil || !valid {
			t.Fatalf("attachment bounds/ownership at width %d: valid=%v err=%v", width, valid, err)
		}
	}
	// A direct refresh must update both mounted owners, not only description files.
	repo.mu.Lock()
	repo.files = repo.files[1:]
	repo.files[0].Name = "refreshed.png"
	repo.mu.Unlock()
	var valid bool
	if err := chromedp.Run(tab, chromedp.Evaluate(`(async () => { const board=document.querySelector('looster-board'), preview=document.querySelector('[data-comment-id="42"] .comment-attachments img'); await board.loadAttachments(1,board.outputs); await board.loadDetail(1); return preview===document.querySelector('[data-comment-id="42"] .comment-attachments img') && !document.querySelector('#description-attachments li') && document.querySelector('[data-comment-id="42"] .comment-attachments a').textContent==='refreshed.png'; })()`, &valid, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil || !valid {
		t.Fatalf("refresh owners: valid=%v err=%v", valid, err)
	}
}

func (r *browserAttachmentRepo) Attachments(_ context.Context, id int64) ([]task.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var files []task.Attachment
	for _, file := range r.files {
		if file.TaskID == id {
			files = append(files, file)
		}
	}
	return files, nil
}
func (r *browserAttachmentRepo) Task(_ context.Context, id int64) (task.Task, error) {
	return task.Task{ID: id, Name: "Upload task", Status: task.Todo}, nil
}
func (r *browserAttachmentRepo) Comment(context.Context, int64, string) (int64, error) {
	return 42, nil
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
	actions := []chromedp.Action{
		chromedp.Navigate(server.URL + "/#board"),
		chromedp.Click("#toggle-stages"),
		chromedp.WaitVisible("task-card"),
		chromedp.Click("task-card"),
		chromedp.WaitVisible("#description-attachments img"),
		wait(`document.querySelector('#description-attachments img')?.naturalWidth === 3`),
		wait(`!document.querySelector('#description-upload') && !document.querySelector('[data-comment-id="42"] input[type=file]')`),
		chromedp.SendKeys(`#comment-form textarea`, "New comment with files"),
		chromedp.SetUploadFiles(`#comment-form input[type=file]`, []string{file}),
		chromedp.Click(`#comment-form button`),
		wait(`document.querySelector('[data-comment-id="42"] .comment-attachments img')?.naturalWidth === 3`),
		wait(`document.querySelectorAll('#description-attachments li').length === 1 && document.querySelectorAll('[data-comment-id="42"] .comment-attachments li').length === 1`),
		chromedp.Evaluate(`document.querySelector('#close').click()`, nil),
		chromedp.Evaluate(`document.querySelector('#new-task').click()`, nil),
		chromedp.WaitVisible(`#create-dialog[open]`),
		chromedp.SendKeys(`#create-form input[name="name"]`, "Task with files"),
		chromedp.SetUploadFiles(`#create-form input[type=file]`, []string{file}),
		chromedp.Evaluate(`document.querySelector('#create-form').requestSubmit()`, nil),
		wait(`document.querySelector('#description-attachments img')?.naturalWidth === 3 && document.querySelector('#dialog-title').textContent.startsWith('TSK-2')`),
		wait(`document.querySelector('#description-attachments a[download]')?.href.includes('/TSK-2/attachments/')`),
	}
	for i, action := range actions {
		if err := chromedp.Run(tab, action); err != nil {
			var state string
			_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({detail:document.querySelector('looster-board').detailID,create:document.querySelector('#create-dialog').outerHTML,panel:document.querySelector('.detail').innerHTML})`, &state))
			t.Log(state)
			t.Fatalf("browser step %d: %v", i, err)
		}
	}
	files, _ := repo.Attachments(ctx, 1)
	if len(files) != 2 || files[1].EventID == nil || *files[1].EventID != 42 {
		t.Fatalf("wrong persisted grouping: %+v", files)
	}
	created, _ := repo.Attachments(ctx, 2)
	if len(created) != 1 || created[0].EventID != nil {
		t.Fatalf("wrong new-task grouping: %+v", created)
	}
}
