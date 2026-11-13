package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
)

// Use the application port so permission-wrapped dashboard repositories retain
// the same run inspection capabilities as the underlying namespace store.
type sessionHistoryRepository = app.SessionHistoryRepository

func registerAgentRuns(mux *http.ServeMux, repo Repository, write func(http.ResponseWriter, *http.Request, any, error)) {
	registerAgentRunOutput(mux, repo, write)
	mux.HandleFunc("GET /api/agents/{name}/runs", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !agent.ValidName(name) {
			http.Error(w, "Invalid agent name", http.StatusBadRequest)
			return
		}
		limit := 20
		if value, present := r.URL.Query()["limit"]; present {
			n, err := strconv.Atoi(value[0])
			if err != nil || n < 1 || n > 100 {
				http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
				return
			}
			limit = n
		}
		var before int64
		if value, present := r.URL.Query()["before_job_id"]; present {
			n, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil || n < 1 {
				http.Error(w, "Invalid before_job_id", http.StatusBadRequest)
				return
			}
			before = n
		}
		history, ok := repo.(sessionHistoryRepository)
		if !ok {
			http.Error(w, "Run history unavailable", http.StatusNotImplemented)
			return
		}
		runs, err := history.SessionHistory(r.Context(), name, 0, before, limit+1)
		if errors.Is(err, app.ErrSessionHistoryUnavailable) {
			http.Error(w, "Run history unavailable", http.StatusNotImplemented)
			return
		}
		if err != nil {
			write(w, r, nil, err)
			return
		}
		if runs == nil {
			runs = []agent.SessionRun{}
		}
		more := len(runs) > limit
		if more {
			runs = runs[:limit]
		}
		var next int64
		if more {
			next = runs[len(runs)-1].JobID
		}
		write(w, r, map[string]any{"runs": runs, "has_more": more, "next_before_job_id": next}, nil)
	})
	mux.HandleFunc("GET /api/agents/{name}/runs/{jobID}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		id, err := strconv.ParseInt(r.PathValue("jobID"), 10, 64)
		if !agent.ValidName(name) || err != nil || id < 1 {
			http.Error(w, "Invalid run identity", http.StatusBadRequest)
			return
		}
		history, ok := repo.(sessionHistoryRepository)
		if !ok {
			http.Error(w, "Run history unavailable", http.StatusNotImplemented)
			return
		}
		run, err := history.SessionRun(r.Context(), id)
		if errors.Is(err, app.ErrSessionHistoryUnavailable) {
			http.Error(w, "Run history unavailable", http.StatusNotImplemented)
			return
		}
		if err == nil && run.Agent != name {
			err = app.ErrNotFound
		}
		write(w, r, run, err)
	})
}
