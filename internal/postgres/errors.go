package postgres

import (
	"errors"

	"github.com/dominikbb/looster/internal/app"
	"github.com/jackc/pgx/v5"
)

// Translate driver-specific absence at the persistence boundary while preserving
// its identity for existing callers using errors.Is(err, pgx.ErrNoRows).
func repositoryError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.Join(app.ErrNotFound, err)
	}
	return err
}
