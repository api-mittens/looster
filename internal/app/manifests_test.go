package app

import (
	"context"
	"errors"
	"testing"

	"github.com/dominikbb/looster/internal/behaviour"
	"github.com/dominikbb/looster/internal/memory"
	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

type manifestPermissionRepo struct {
	Repository
	calls  int
	source string
	id     int64
}

func (r *manifestPermissionRepo) ApplyManifest(_ context.Context, source string) (behaviour.Manifest, error) {
	r.calls++
	r.source = source
	return behaviour.Manifest{ID: 7, Source: source}, nil
}
func (r *manifestPermissionRepo) Manifests(context.Context) ([]behaviour.Manifest, error) {
	r.calls++
	return []behaviour.Manifest{{ID: 7, Source: r.source}}, nil
}
func (r *manifestPermissionRepo) ActivateManifest(_ context.Context, id int64) error {
	r.calls++
	r.id = id
	return nil
}

func TestManifestPermissionsForwardOptionalPort(t *testing.T) {
	ctx := context.Background()
	for _, role := range []Role{Human, Agent} {
		underlying := &manifestPermissionRepo{}
		wrapped, err := WithPermissions(underlying, Actor{Role: role, Name: "coder"})
		if err != nil {
			t.Fatal(err)
		}
		repo, ok := wrapped.(ManifestRepository)
		if !ok {
			t.Fatal("permission wrapper hides manifest port")
		}
		source := "-- Preserve exact bytes\nAGENT coder WITH \"Work\";\n"
		m, err := repo.ApplyManifest(ctx, source)
		if role == Agent {
			if !errors.Is(err, ErrForbidden) || underlying.calls != 0 {
				t.Fatal("agent could apply manifest")
			}
		} else if err != nil || m.Source != source || underlying.source != source {
			t.Fatalf("source not forwarded: %+v %v", m, err)
		}
		before := underlying.calls
		err = repo.ActivateManifest(ctx, 7)
		if role == Agent {
			if !errors.Is(err, ErrForbidden) || underlying.calls != before {
				t.Fatal("agent could activate manifest")
			}
		} else if err != nil || underlying.id != 7 {
			t.Fatalf("activate not forwarded: %v", err)
		}
		versions, err := repo.Manifests(ctx)
		if err != nil || len(versions) != 1 {
			t.Fatalf("read failed: %+v %v", versions, err)
		}
	}
	wrapped, err := WithPermissions(fakeRepo{}, Actor{Role: Human})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.(ManifestRepository).ApplyManifest(ctx, ""); err == nil {
		t.Fatal("missing optional port accepted")
	}
}

type inactiveContextRepo struct {
	fakeRepo
	memoryCalls int
}

func (r *inactiveContextRepo) Tag(_ context.Context, name string) (tag.Tag, error) {
	return tag.Tag{Name: name, Description: "Stale instructions", Inactive: true}, nil
}
func (r *inactiveContextRepo) Memories(context.Context, string, string) ([]memory.Memory, error) {
	r.memoryCalls++
	return nil, errors.New("inactive memories must not be loaded")
}
func TestInactiveTagContextRetainsTaskWithoutInstructions(t *testing.T) {
	repo := &inactiveContextRepo{}
	task := task.Task{Tags: []string{"old/tag"}}
	expanded, err := ExpandTaskContext(context.Background(), repo, task, "")
	if err != nil || len(expanded.Tags) != 0 || repo.memoryCalls != 0 || len(task.Tags) != 1 {
		t.Fatalf("inactive context leaked: %+v %v", expanded, err)
	}
}
