// Package web serves the task dashboard and its JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/behaviour"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/tag"
	"github.com/api-mittens/looster/internal/task"
)

//go:embed static/*
var assets embed.FS

type Repository interface {
	app.ContextRepository
	Tasks(context.Context) ([]task.Task, error)
	Task(context.Context, int64) (task.Task, error)
	Agents(context.Context) ([]agent.Agent, error)
	Sessions(context.Context, string) ([]agent.Session, error)
	Logs(context.Context, event.Filter) ([]event.Event, error)
	Workers(context.Context) ([]app.Worker, error)
	Behaviour(context.Context) ([]behaviour.Rule, error)
	Tags(context.Context) ([]tag.Tag, error)
	CreateTask(context.Context, string, string, task.Status, []string) (task.Task, error)
	SetStatus(context.Context, int64, task.Status) error
	Comment(context.Context, int64, string) (int64, error)
}

// NamespaceProvider resolves a namespace into an isolated repository.
type NamespaceProvider interface {
	Namespaces(context.Context) ([]string, error)
	Repository(context.Context, string) (Repository, error)
}

type namespaceAdapter struct{ app.NamespaceProvider }

func (p namespaceAdapter) DefaultNamespace() string { return p.NamespaceProvider.DefaultNamespace() }

func (p namespaceAdapter) Repository(ctx context.Context, name string) (Repository, error) {
	return p.NamespaceProvider.Repository(ctx, name)
}

// WithNamespaces bridges the application namespace port without exposing storage
// implementations to the dashboard package.
func WithNamespaces(provider app.NamespaceProvider) NamespaceProvider {
	return namespaceAdapter{provider}
}

func Handler(repo Repository, providers ...NamespaceProvider) http.Handler {
	return HandlerWithStorage(repo, nil, providers...)
}

// HandlerWithStorage enables dashboard attachments using the application's object port.
func HandlerWithStorage(repo Repository, objects app.Objects, providers ...NamespaceProvider) http.Handler {
	selected := namespace.Default
	if len(providers) > 0 {
		if defaults, ok := providers[0].(interface{ DefaultNamespace() string }); ok {
			selected = defaults.DefaultNamespace()
		}
	}
	base := dashboardHandler(repo, objects, selected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/namespaces" && r.Method == http.MethodGet {
			names := []string{}
			if len(providers) > 0 {
				var err error
				names, err = providers[0].Namespaces(r.Context())
				if err != nil {
					http.Error(w, "Could not load namespaces", 500)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if len(providers) > 0 {
				if defaults, ok := providers[0].(interface{ DefaultNamespace() string }); ok {
					w.Header().Set("X-Default-Namespace", defaults.DefaultNamespace())
				}
			}
			_ = json.NewEncoder(w).Encode(names)
			return
		}
		if namespace := r.URL.Query().Get("namespace"); namespace != "" && strings.HasPrefix(r.URL.Path, "/api/") {
			if len(providers) == 0 {
				http.Error(w, "Namespace selection unavailable", 400)
				return
			}
			scoped, err := providers[0].Repository(r.Context(), namespace)
			if err != nil {
				http.Error(w, "Namespace not found", 404)
				return
			}
			dashboardHandler(scoped, objects, namespace).ServeHTTP(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}

func dashboardHandler(repo Repository, objects app.Objects, selected string) http.Handler {
	mux := http.NewServeMux()
	registerAttachments(mux, repo, objects, selected)
	decode := func(w http.ResponseWriter, r *http.Request, dst any) bool {
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
			return false
		}
		return true
	}
	write := func(w http.ResponseWriter, data any, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			slog.Error("dashboard API query failed", "error", err)
			code, message := http.StatusInternalServerError, "Could not load dashboard data"
			if errors.Is(err, app.ErrNotFound) {
				code, message = http.StatusNotFound, "Resource not found"
			} else if errors.Is(err, app.ErrForbidden) {
				code, message = http.StatusForbidden, "Permission denied"
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	}
	registerAgentRuns(mux, repo, write)
	mux.HandleFunc("GET /api/manifests", func(w http.ResponseWriter, r *http.Request) {
		manifests, ok := repo.(app.ManifestRepository)
		if !ok {
			write(w, []behaviour.Manifest{}, nil)
			return
		}
		versions, err := manifests.Manifests(r.Context())
		write(w, versions, err)
	})
	mux.HandleFunc("POST /api/manifests/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid manifest ID", 400)
			return
		}
		var input struct{}
		if !decode(w, r, &input) {
			return
		}
		manifests, ok := repo.(app.ManifestRepository)
		if !ok {
			http.Error(w, "Manifest versions unavailable", 501)
			return
		}
		if err := manifests.ActivateManifest(r.Context(), id); err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]int64{"id": id}, nil)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		workers, err := repo.Workers(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		agents, err := repo.Agents(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		sessions, err := repo.Sessions(r.Context(), "")
		if err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]any{"workers": workers, "agents": agents, "sessions": sessions, "heartbeat_window_seconds": 30}, nil)
	})
	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		rules, err := repo.Behaviour(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]any{"behaviours": rules}, nil)
	})
	mux.HandleFunc("GET /api/library", func(w http.ResponseWriter, r *http.Request) {
		agents, err := repo.Agents(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		tags, err := repo.Tags(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		agentViews := []app.AgentContext{}
		tagViews := []app.TagContext{}
		for _, a := range agents {
			m, err := repo.Memories(r.Context(), "agent", a.Name)
			if err != nil {
				write(w, nil, err)
				return
			}
			agentViews = append(agentViews, app.AgentContext{Agent: a, Memories: m})
		}
		for _, t := range tags {
			m, err := repo.Memories(r.Context(), "tag", t.Name)
			if err != nil {
				write(w, nil, err)
				return
			}
			tagViews = append(tagViews, app.TagContext{Tag: t, Memories: m})
		}
		write(w, map[string]any{"agents": agentViews, "tags": tagViews}, nil)
	})
	mux.HandleFunc("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		tasks, err := repo.Tasks(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		agents, err := repo.Agents(r.Context())
		if err != nil {
			write(w, nil, err)
			return
		}
		sessions, err := repo.Sessions(r.Context(), "")
		if err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]any{"tasks": tasks, "agents": agents, "sessions": sessions}, nil)
	})
	mux.HandleFunc("POST /api/tasks", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			Status      task.Status `json:"status"`
			Tags        []string    `json:"tags"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" {
			http.Error(w, "Task name is required", 400)
			return
		}
		if input.Status == "" {
			input.Status = task.Planning
		}
		if err := input.Status.Validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if len(input.Tags) > 0 {
			tags, err := repo.Tags(r.Context())
			if err != nil {
				write(w, nil, err)
				return
			}
			defined := make(map[string]bool, len(tags))
			for _, t := range tags {
				defined[t.Name] = true
			}
			for _, name := range input.Tags {
				if !defined[name] {
					http.Error(w, "Task tags must be defined in the active manifest", http.StatusBadRequest)
					return
				}
			}
		}
		created, err := repo.CreateTask(r.Context(), input.Name, input.Description, input.Status, input.Tags)
		if err != nil {
			write(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		write(w, created, nil)
	})
	mux.HandleFunc("POST /api/tasks/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", http.StatusBadRequest)
			return
		}
		var input struct {
			Status task.Status `json:"status"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := input.Status.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := repo.SetStatus(r.Context(), id, input.Status); err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]task.Status{"status": input.Status}, nil)
	})
	mux.HandleFunc("POST /api/tasks/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		var input struct {
			Body string `json:"body"`
		}
		if !decode(w, r, &input) {
			return
		}
		if strings.TrimSpace(input.Body) == "" {
			http.Error(w, "Comment is required", 400)
			return
		}
		if _, err := repo.Task(r.Context(), id); err != nil {
			write(w, nil, err)
			return
		}
		eventID, err := repo.Comment(r.Context(), id, input.Body)
		if err != nil {
			write(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		write(w, map[string]int64{"event_id": eventID}, nil)
	})
	mux.HandleFunc("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		t, err := repo.Task(r.Context(), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		sessions, err := repo.Sessions(r.Context(), "")
		if err != nil {
			write(w, nil, err)
			return
		}
		selected := []agent.Session{}
		for _, s := range sessions {
			if s.TaskID == id {
				selected = append(selected, s)
			}
		}
		expanded, err := app.ExpandTaskContext(r.Context(), repo, t, "")
		if err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]any{"task": t, "sessions": selected, "context": expanded}, nil)
	})
	mux.HandleFunc("GET /api/tasks/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		id, err := task.ParseID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid task ID", 400)
			return
		}
		after := int64(0)
		if value := r.URL.Query().Get("after"); value != "" {
			after, err = strconv.ParseInt(value, 10, 64)
			if err != nil || after < 0 {
				http.Error(w, "Invalid event cursor", 400)
				return
			}
		}
		if _, err = repo.Task(r.Context(), id); err != nil {
			write(w, nil, err)
			return
		}
		// Initial page shows recent activity; subsequent pages follow the cursor forward.
		var kinds []string
		if kind := r.URL.Query().Get("kind"); kind != "" {
			if kind != "comment" {
				http.Error(w, "Invalid event kind", 400)
				return
			}
			kinds = []string{"comment"}
		}
		events, err := repo.Logs(r.Context(), event.Filter{TaskID: id, After: after, Kinds: kinds, Limit: 200, Desc: after == 0 && r.URL.Query().Get("after") == ""})
		if err == nil && after == 0 && r.URL.Query().Get("after") == "" {
			for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
				events[i], events[j] = events[j], events[i]
			}
		}
		write(w, events, err)
	})
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return mux
}
