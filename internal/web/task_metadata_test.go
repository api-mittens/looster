package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

type metadataTestRepo struct {
	testRepo
	payloadCalls int
}

func (*metadataTestRepo) Tasks(context.Context) ([]task.Task, error)     { panic("full task read") }
func (*metadataTestRepo) Task(context.Context, int64) (task.Task, error) { panic("full task read") }
func (*metadataTestRepo) Sessions(context.Context, string) ([]agent.Session, error) {
	panic("full session read")
}
func (*metadataTestRepo) TasksMetadata(context.Context) ([]task.Task, error) {
	return []task.Task{{ID: 1, Name: "Metadata"}}, nil
}
func (*metadataTestRepo) TaskMetadata(context.Context, int64) (task.Task, error) {
	return task.Task{ID: 1, Name: "Metadata"}, nil
}
func (*metadataTestRepo) SessionsMetadata(context.Context, string) ([]agent.Session, error) {
	return []agent.Session{}, nil
}
func (r *metadataTestRepo) TaskPayloadDetail(context.Context, int64, int64, int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	r.payloadCalls++
	return task.Task{}, nil, nil, errors.New("payload read failed")
}

func TestMetadataHTTPDoesNotReadPayloads(t *testing.T) {
	repo := &metadataTestRepo{}
	handler := Handler(repo)
	for _, path := range []string{"/api/board", "/api/board/list", "/api/status", "/api/tasks/TSK-1", "/api/tasks/TSK-1/events?kind=comment"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: status %d: %s", path, response.Code, response.Body.String())
		}
	}
	if repo.payloadCalls != 0 {
		t.Fatalf("metadata payload calls: %d", repo.payloadCalls)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/payloads", nil))
	if response.Code != 500 || repo.payloadCalls != 1 {
		t.Fatalf("payload failure status=%d calls=%d", response.Code, repo.payloadCalls)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1", nil))
	if response.Code != 200 {
		t.Fatalf("metadata after payload failure: %d", response.Code)
	}
}

func TestTaskPayloadBoundsHTTP(t *testing.T) {
	repo := &metadataTestRepo{}
	handler := Handler(repo)
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=x", "?before_job_id=-1", "?before_job_id=x", "?before_log_id=-1", "?log_limit=201", "?log_limit=0"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/payloads"+suffix, nil))
		if response.Code != 400 {
			t.Fatalf("%s: %d", suffix, response.Code)
		}
	}
	if repo.payloadCalls != 0 {
		t.Fatalf("invalid requests read payloads: %d", repo.payloadCalls)
	}
}

func TestMetadataBackgroundAndCommentChecks(t *testing.T) {
	repo := &metadataTestRepo{}
	handler := Handler(repo)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/attachments", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("attachments: %d: %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/tasks/TSK-1/comments", strings.NewReader(`{"body":"Test comment"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("comment: %d: %s", response.Code, response.Body.String())
	}
	if repo.payloadCalls != 0 {
		t.Fatalf("metadata checks read payloads: %d", repo.payloadCalls)
	}
}

type pagedMetadataRepo struct {
	metadataTestRepo
	before, beforeLog int64
}

func (r *pagedMetadataRepo) TaskPayloadDetailPage(_ context.Context, id, before, beforeLog int64, limit, logLimit int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	r.before, r.beforeLog = before, beforeLog
	return task.Task{ID: id, SessionLogs: []agent.SessionLog{{ID: 3, Body: "Log text"}}}, nil, []agent.SessionRun{{JobID: 7}}, nil
}

func TestTaskPayloadPageHTTP(t *testing.T) {
	repo := &pagedMetadataRepo{}
	response := httptest.NewRecorder()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/payloads?before_job_id=8&before_log_id=4&limit=1&log_limit=1", nil))
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var page struct {
		Next     int64 `json:"next_before_job_id"`
		NextLog  int64 `json:"next_before_log_id"`
		More     bool  `json:"has_more"`
		MoreLogs bool  `json:"has_more_logs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if repo.before != 8 || repo.beforeLog != 4 || page.Next != 7 || page.NextLog != 3 || !page.More || !page.MoreLogs {
		t.Fatalf("page %+v cursors %d %d", page, repo.before, repo.beforeLog)
	}
}

type delayedMetadataRepo struct {
	metadataTestRepo
	started chan struct{}
	release chan struct{}
}

func (r *delayedMetadataRepo) TaskPayloadDetail(ctx context.Context, id, before int64, limit int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	close(r.started)
	select {
	case <-ctx.Done():
		return task.Task{}, nil, nil, ctx.Err()
	case <-r.release:
		return task.Task{ID: id}, nil, nil, nil
	}
}

func TestMetadataHTTPDuringDelayedPayload(t *testing.T) {
	repo := &delayedMetadataRepo{started: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewServer(Handler(repo))
	defer server.Close()
	defer close(repo.release)
	done := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL + "/api/tasks/TSK-1/payloads")
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-repo.started:
	case <-time.After(5 * time.Second):
		t.Fatal("payload request did not start")
	}
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/api/board", "/api/tasks/TSK-1", "/api/status"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("payload request was not delayed: %v", err)
	default:
	}
}
