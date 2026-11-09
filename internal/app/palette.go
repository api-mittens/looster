package app

import (
	"context"

	"github.com/api-mittens/looster/internal/task"
)

// PaletteRepository returns the globally best fifty task candidates, ranked
// before limiting, without hydrating task histories or sessions.
type PaletteRepository interface {
	PaletteTasks(context.Context, string) ([]task.Task, error)
}

func (r *accessRepository) PaletteTasks(ctx context.Context, search string) ([]task.Task, error) {
	repo, ok := r.Repository.(PaletteRepository)
	if !ok {
		return nil, ErrColumnPaginationUnavailable
	}
	return repo.PaletteTasks(ctx, search)
}
