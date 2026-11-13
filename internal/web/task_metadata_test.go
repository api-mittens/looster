package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

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
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=x", "?before_job_id=-1", "?before_job_id=x"} {
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
