package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func metadataTask(ctx context.Context, repo Repository, id int64) (task.Task, error) {
	if source, ok := repo.(app.TaskMetadataRepository); ok {
		return source.TaskMetadata(ctx, id)
	}
	return repo.Task(ctx, id)
}
func metadataTasks(ctx context.Context, repo Repository) ([]task.Task, error) {
	if source, ok := repo.(app.TaskMetadataRepository); ok {
		return source.TasksMetadata(ctx)
	}
	return repo.Tasks(ctx)
}
func metadataSessions(ctx context.Context, repo Repository) ([]agent.Session, error) {
	if source, ok := repo.(app.TaskMetadataRepository); ok {
		return source.SessionsMetadata(ctx, "")
	}
	return repo.Sessions(ctx, "")
}

func registerTaskPayloads(mux *http.ServeMux, repo Repository, write func(http.ResponseWriter, any, error)) {
	mux.HandleFunc("GET /api/tasks/{id}/payloads", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 200 {
				http.Error(w, "Invalid page limit", 400)
				return
			}
		}
		var before int64
		if raw := r.URL.Query().Get("before_job_id"); raw != "" {
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 1 {
				http.Error(w, "Invalid page cursor", 400)
				return
			}
		}
		var beforeLog int64
		if raw := r.URL.Query().Get("before_log_id"); raw != "" {
			beforeLog, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || beforeLog < 1 {
				http.Error(w, "Invalid log cursor", 400)
				return
			}
		}
		logLimit := 200
		if raw := r.URL.Query().Get("log_limit"); raw != "" {
			logLimit, err = strconv.Atoi(raw)
			if err != nil || logLimit < 1 || logLimit > 200 {
				http.Error(w, "Invalid log page limit", 400)
				return
			}
		}
		source, ok := repo.(app.TaskMetadataRepository)
		if !ok {
			http.Error(w, "Task payloads unavailable", 501)
			return
		}
		var t task.Task
		var sessions []agent.Session
		var runs []agent.SessionRun
		if pages, ok := repo.(app.TaskPayloadPageRepository); ok {
			t, sessions, runs, err = pages.TaskPayloadDetailPage(r.Context(), id, before, beforeLog, limit, logLimit)
		} else {
			if beforeLog != 0 {
				http.Error(w, "Task log pages unavailable", 501)
				return
			}
			t, sessions, runs, err = source.TaskPayloadDetail(r.Context(), id, before, limit)
		}
		if err != nil {
			write(w, nil, err)
			return
		}
		if sessions == nil {
			sessions = []agent.Session{}
		}
		if runs == nil {
			runs = []agent.SessionRun{}
		}
		var next int64
		if len(runs) == limit {
			next = runs[len(runs)-1].JobID
		}
		var nextLog int64
		if len(t.SessionLogs) == logLimit {
			nextLog = t.SessionLogs[0].ID
		}
		write(w, map[string]any{"task": t, "sessions": sessions, "runs": runs, "has_more": next != 0, "next_before_job_id": next, "has_more_logs": nextLog != 0, "next_before_log_id": nextLog}, nil)
	})
}
