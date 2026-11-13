package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func listBoard(repo Repository, write func(http.ResponseWriter, *http.Request, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pager, ok := repo.(app.ColumnRepository)
		if !ok {
			http.Error(w, "Column pagination unavailable", http.StatusNotImplemented)
			return
		}
		pages := map[string]int{}
		if raw := r.URL.Query().Get("pages"); raw != "" {
			if len(raw) > 16384 || json.Unmarshal([]byte(raw), &pages) != nil {
				http.Error(w, "Invalid column pages", 400)
				return
			}
			for _, page := range pages {
				if page < 1 {
					http.Error(w, "Pages must be positive", 400)
					return
				}
			}
		}
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if len(search) > 4096 {
			http.Error(w, "Search too long", 400)
			return
		}
		agents, err := repo.Agents(r.Context())
		if err != nil {
			write(w, r, nil, err)
			return
		}
		sessions, err := pager.BoardSessions(r.Context())
		if err != nil {
			write(w, r, nil, err)
			return
		}
		// Match boardAgentNames: taskless-only agents stay in Other sessions unless
		// they also have a durable task binding. Liveness is resolved by PostgreSQL.
		names := map[string]bool{}
		standalone := map[string]bool{}
		taskAgents := map[string]bool{}
		active := map[string][]int64{}
		for _, a := range agents {
			names[a.Name] = true
			if a.TasklessOnly {
				standalone[a.Name] = true
			}
		}
		for _, s := range sessions {
			names[s.Agent] = true
			if s.TaskID == 0 {
				standalone[s.Agent] = true
			} else {
				taskAgents[s.Agent] = true
				if s.Status == "running" {
					active[s.Agent] = append(active[s.Agent], s.TaskID)
				}
			}
		}
		ordered := []string{}
		for name := range names {
			if name != "" && (!standalone[name] || taskAgents[name]) {
				ordered = append(ordered, name)
			}
		}
		sort.Strings(ordered)
		keys := []string{"planning", "todo", "validated", "closed"}
		for _, name := range ordered {
			keys = append(keys, "agent:"+name)
		}
		columns := make([]task.ColumnPage, 0, len(keys))
		for _, key := range keys {
			page := pages[key]
			if page == 0 {
				page = 1
			}
			column, err := pager.ColumnTasks(r.Context(), task.ColumnQuery{Key: key, Search: search, Fuzzy: r.URL.Query().Get("fuzzy") == "true", Page: page, ActiveTaskIDs: active[strings.TrimPrefix(key, "agent:")]})
			if err != nil {
				write(w, r, nil, err)
				return
			}
			columns = append(columns, column)
		}
		write(w, r, map[string]any{"tasks": []task.Task{}, "agents": agents, "sessions": sessions, "columns": columns}, nil)
	}
}
