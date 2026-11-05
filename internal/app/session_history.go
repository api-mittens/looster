package app

import (
	"context"
	"errors"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/event"
)

// SessionHistoryRepository exposes read-only, namespace-scoped run inspection.
// It remains optional for repositories without durable session storage.
type SessionHistoryRepository interface {
	SessionHistory(context.Context, string, int64, int64, int) ([]agent.SessionRun, error)
	SessionRun(context.Context, int64) (agent.SessionRun, error)
}

// SessionRunOutputRepository reads output explicitly attributed to one job.
// Older uncorrelated event logs must not be guessed from a shared session ID.
type SessionRunOutputRepository interface {
	SessionRunOutput(context.Context, int64, int64, int) ([]event.Event, error)
}

func (r *accessRepository) SessionRunOutput(ctx context.Context, jobID, after int64, limit int) ([]event.Event, error) {
	repo, ok := r.Repository.(SessionRunOutputRepository)
	if !ok {
		return nil, ErrSessionHistoryUnavailable
	}
	return repo.SessionRunOutput(ctx, jobID, after, limit)
}

var ErrSessionHistoryUnavailable = errors.New("run history unavailable")

func (r *accessRepository) SessionHistory(ctx context.Context, name string, taskID, beforeJobID int64, limit int) ([]agent.SessionRun, error) {
	repo, ok := r.Repository.(SessionHistoryRepository)
	if !ok {
		return nil, ErrSessionHistoryUnavailable
	}
	return repo.SessionHistory(ctx, name, taskID, beforeJobID, limit)
}

func (r *accessRepository) SessionRun(ctx context.Context, jobID int64) (agent.SessionRun, error) {
	repo, ok := r.Repository.(SessionHistoryRepository)
	if !ok {
		return agent.SessionRun{}, ErrSessionHistoryUnavailable
	}
	return repo.SessionRun(ctx, jobID)
}
