package app

import (
	"context"
	"errors"

	"github.com/api-mittens/looster/internal/agent"
)

// SessionHistoryRepository exposes read-only, namespace-scoped run inspection.
// It remains optional for repositories without durable session storage.
type SessionHistoryRepository interface {
	SessionHistory(context.Context, string, int64, int64, int) ([]agent.SessionRun, error)
	SessionRun(context.Context, int64) (agent.SessionRun, error)
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
