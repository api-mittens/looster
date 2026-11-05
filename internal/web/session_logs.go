package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func registerSessionLogs(mux *http.ServeMux, repo Repository, write func(http.ResponseWriter, any, error), decode func(http.ResponseWriter, *http.Request, any) bool) {
	logs, available := repo.(app.SessionLogRepository)
	unavailable := func(w http.ResponseWriter) bool {
		if !available {
			http.Error(w, "Session logs unavailable", http.StatusNotImplemented)
			return true
		}
		return false
	}
	respond := func(w http.ResponseWriter, data any, err error) {
		if errors.Is(err, app.ErrInvalidSessionLog) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, app.ErrSessionHistoryUnavailable) {
			http.Error(w, "Session logs unavailable", http.StatusNotImplemented)
			return
		}
		write(w, data, err)
	}
	mux.HandleFunc("GET /api/tasks/{id}/session-logs", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		if unavailable(w) {
			return
		}
		if _, err := repo.Task(r.Context(), id); err != nil {
			write(w, nil, err)
			return
		}
		rows, err := logs.TaskSessionLogs(r.Context(), id)
		if rows == nil {
			rows = []agent.SessionLog{}
		}
		respond(w, map[string]any{"logs": rows}, err)
	})
	run := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("jobID"), 10, 64)
		if err != nil || id < 1 || !agent.ValidName(r.PathValue("name")) {
			http.Error(w, "Invalid run identity", 400)
			return 0, false
		}
		if unavailable(w) {
			return 0, false
		}
		history, ok := repo.(app.SessionHistoryRepository)
		if !ok {
			http.Error(w, "Run history unavailable", 501)
			return 0, false
		}
		found, err := history.SessionRun(r.Context(), id)
		if err == nil && found.Agent != r.PathValue("name") {
			err = app.ErrNotFound
		}
		if err != nil {
			respond(w, nil, err)
			return 0, false
		}
		return id, true
	}
	mux.HandleFunc("GET /api/agents/{name}/runs/{jobID}/logs", func(w http.ResponseWriter, r *http.Request) {
		id, ok := run(w, r)
		if !ok {
			return
		}
		rows, err := logs.RunSessionLogs(r.Context(), id)
		if rows == nil {
			rows = []agent.SessionLog{}
		}
		respond(w, map[string]any{"logs": rows}, err)
	})
	mux.HandleFunc("POST /api/agents/{name}/runs/{jobID}/logs", func(w http.ResponseWriter, r *http.Request) {
		id, ok := run(w, r)
		if !ok {
			return
		}
		var body struct {
			Kind      string `json:"kind"`
			Body      string `json:"body"`
			SessionID string `json:"session_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		result, err := logs.AppendSessionLog(r.Context(), agent.SessionLogInput{JobID: id, Agent: r.PathValue("name"), Kind: body.Kind, Body: body.Body, SessionID: body.SessionID})
		respond(w, result, err)
	})
}
