package app

import (
	"context"
	"errors"
	"github.com/api-mittens/looster/internal/task"
	"testing"
)

type palettePermissionRepo struct {
	Repository
	query   string
	failure error
}

func (r *palettePermissionRepo) PaletteTasks(_ context.Context, q string) ([]task.Task, error) {
	r.query = q
	return []task.Task{{ID: 9}}, r.failure
}
func (*palettePermissionRepo) Tasks(context.Context) ([]task.Task, error) {
	panic("palette must not hydrate all tasks")
}
func TestPermissionPaletteForwarding(t *testing.T) {
	for _, role := range []Role{Human, Agent} {
		raw := &palettePermissionRepo{}
		wrapped, err := WithPermissions(raw, Actor{Role: role, Name: "coder"})
		if err != nil {
			t.Fatal(err)
		}
		reader, ok := wrapped.(PaletteRepository)
		if !ok {
			t.Fatal("palette port dropped")
		}
		got, err := reader.PaletteTasks(context.Background(), "needle")
		if err != nil || len(got) != 1 || raw.query != "needle" {
			t.Fatalf("forwarding: %v %v", got, err)
		}
		raw.failure = errors.New("storage failure")
		if _, err = reader.PaletteTasks(context.Background(), "x"); !errors.Is(err, raw.failure) {
			t.Fatal(err)
		}
	}
	wrapped, err := WithPermissions(struct{ Repository }{}, Actor{Role: Human})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrapped.(PaletteRepository).PaletteTasks(context.Background(), ""); !errors.Is(err, ErrColumnPaginationUnavailable) {
		t.Fatal(err)
	}
}
