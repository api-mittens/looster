package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
)

func registerAgentRunOutput(mux *http.ServeMux, repo Repository, write func(http.ResponseWriter, any, error)) {
	mux.HandleFunc("GET /api/agents/{name}/runs/{jobID}/output", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		id, err := strconv.ParseInt(r.PathValue("jobID"), 10, 64)
		if !agent.ValidName(name) || err != nil || id < 1 {
			http.Error(w, "Invalid run identity", 400)
			return
		}
		limit := 200
		if raw, present := r.URL.Query()["limit"]; present {
			value, err := strconv.Atoi(raw[0])
			if err != nil || value < 1 || value > 200 {
				http.Error(w, "limit must be between 1 and 200", 400)
				return
			}
			limit = value
		}
		var after int64
		if raw, present := r.URL.Query()["after"]; present {
			value, err := strconv.ParseInt(raw[0], 10, 64)
			if err != nil || value < 0 {
				http.Error(w, "Invalid after cursor", 400)
				return
			}
			after = value
		}
		history, ok := repo.(app.SessionHistoryRepository)
		output, hasOutput := repo.(app.SessionRunOutputRepository)
		if !ok || !hasOutput {
			http.Error(w, "Run output unavailable", 501)
			return
		}
		run, err := history.SessionRun(r.Context(), id)
		if errors.Is(err, app.ErrSessionHistoryUnavailable) {
			http.Error(w, "Run output unavailable", 501)
			return
		}
		if err == nil && run.Agent != name {
			err = app.ErrNotFound
		}
		if err != nil {
			write(w, nil, err)
			return
		}
		events, err := output.SessionRunOutput(r.Context(), id, after, limit+1)
		if errors.Is(err, app.ErrSessionHistoryUnavailable) {
			http.Error(w, "Run output unavailable", 501)
			return
		}
		if err != nil {
			write(w, nil, err)
			return
		}
		if events == nil {
			events = []event.Event{}
		}
		more := len(events) > limit
		if more {
			events = events[:limit]
		}
		next := after
		if len(events) > 0 {
			next = events[len(events)-1].ID
		}
		write(w, map[string]any{"events": events, "has_more": more, "next_after": next}, nil)
	})
}
