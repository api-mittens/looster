package app

import (
	"context"
	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

// TaskMetadataRepository reads dashboard metadata without lake payloads.
type TaskMetadataRepository interface {
	TasksMetadata(context.Context) ([]task.Task, error)
	TaskMetadata(context.Context, int64) (task.Task, error)
	SessionsMetadata(context.Context, string) ([]agent.Session, error)
	TaskPayloadDetail(context.Context, int64, int64, int) (task.Task, []agent.Session, []agent.SessionRun, error)
}

func (r *accessRepository) TasksMetadata(ctx context.Context) ([]task.Task, error) {
	if repo, ok := r.Repository.(TaskMetadataRepository); ok {
		return repo.TasksMetadata(ctx)
	}
	return nil, ErrSessionHistoryUnavailable
}
func (r *accessRepository) TaskMetadata(ctx context.Context, id int64) (task.Task, error) {
	if repo, ok := r.Repository.(TaskMetadataRepository); ok {
		return repo.TaskMetadata(ctx, id)
	}
	return task.Task{}, ErrSessionHistoryUnavailable
}
func (r *accessRepository) SessionsMetadata(ctx context.Context, name string) ([]agent.Session, error) {
	if repo, ok := r.Repository.(TaskMetadataRepository); ok {
		return repo.SessionsMetadata(ctx, name)
	}
	return nil, ErrSessionHistoryUnavailable
}
func (r *accessRepository) TaskPayloadDetail(ctx context.Context, id, before int64, limit int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	if repo, ok := r.Repository.(TaskMetadataRepository); ok {
		return repo.TaskPayloadDetail(ctx, id, before, limit)
	}
	return task.Task{}, nil, nil, ErrSessionHistoryUnavailable
}
