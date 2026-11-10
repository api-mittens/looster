package cli

import (
	"context"
	"errors"
	"io"

	"github.com/api-mittens/looster/internal/ducklake"
)

// serveWithDuckDB owns exactly one core database for the entire server run,
// including all workers and their shutdown. It does not attach or initialize a
// DuckLake catalog, install extensions or change PostgreSQL application storage.
func serveWithDuckDB(ctx context.Context, serve func(context.Context) error) error {
	return serveWithDatabase(ctx, func(ctx context.Context) (io.Closer, error) {
		return ducklake.OpenEngine(ctx)
	}, serve)
}

func serveWithDatabase(ctx context.Context, open func(context.Context) (io.Closer, error), serve func(context.Context) error) (result error) {
	database, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, database.Close()) }()
	return serve(ctx)
}
