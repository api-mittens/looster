package web

import (
	"context"
	"errors"
	"github.com/api-mittens/looster/internal/task"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type paletteRepo struct {
	Repository
	query   string
	calls   int
	failure error
}

func (r *paletteRepo) PaletteTasks(_ context.Context, q string) ([]task.Task, error) {
	r.query = q
	r.calls++
	return []task.Task{{ID: 99, Name: "needle"}}, r.failure
}
func (*paletteRepo) Tasks(context.Context) ([]task.Task, error) {
	panic("palette must not read unbounded tasks")
}
func TestPaletteEndpoint(t *testing.T) {
	repo := &paletteRepo{Repository: &testRepo{}}
	handler := Handler(repo)
	for _, q := range []string{"needle", "  needle  ", "[.*", ""} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/palette/tasks?search="+url.QueryEscape(q), nil))
		if w.Code != 200 || repo.query != q || !strings.Contains(w.Body.String(), `"id":99`) {
			t.Fatalf("query=%q HTTP %d %s forwarded=%q", q, w.Code, w.Body.String(), repo.query)
		}
	}
	calls := repo.calls
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/palette/tasks?search="+strings.Repeat("x", 4097), nil))
	if w.Code != 400 || repo.calls != calls {
		t.Fatal("oversized query reached repository")
	}
	repo.failure = errors.New("private database info")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/palette/tasks", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("error leak: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	Handler(&testRepo{}).ServeHTTP(w, httptest.NewRequest("GET", "/api/palette/tasks", nil))
	if w.Code != 501 {
		t.Fatalf("unsupported HTTP %d", w.Code)
	}
}
