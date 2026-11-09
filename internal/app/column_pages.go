package app

import (
	"context"
	"errors"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

// ColumnRepository is the optional bounded dashboard List read port. Keep it
// separate from Repository so CLI and detail adapters need not implement it.
// Permission and namespace wrappers must preserve this port explicitly.
type ColumnRepository interface {
	ColumnTasks(context.Context, task.ColumnQuery) (task.ColumnPage, error)
	BoardSessions(context.Context) ([]agent.Session, error)
}

var ErrColumnPaginationUnavailable = errors.New("column pagination unavailable")

var _ ColumnRepository = (*accessRepository)(nil)

func (r *accessRepository) ColumnTasks(ctx context.Context, q task.ColumnQuery) (task.ColumnPage, error) {
	repo, ok := r.Repository.(ColumnRepository)
	if !ok {
		return task.ColumnPage{}, ErrColumnPaginationUnavailable
	}
	return repo.ColumnTasks(ctx, q)
}
func (r *accessRepository) BoardSessions(ctx context.Context) ([]agent.Session, error) {
	repo, ok := r.Repository.(ColumnRepository)
	if !ok {
		return nil, ErrColumnPaginationUnavailable
	}
	return repo.BoardSessions(ctx)
}
