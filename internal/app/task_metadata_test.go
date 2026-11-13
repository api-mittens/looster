package app

import (
	"context"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

type metadataPortRepo struct {
	Repository
	calls int
}

func (r *metadataPortRepo) TasksMetadata(context.Context) ([]task.Task, error) {
	r.calls++
	return []task.Task{{ID: 80}}, nil
}
func (r *metadataPortRepo) TaskMetadata(context.Context, int64) (task.Task, error) {
	r.calls++
	return task.Task{ID: 80}, nil
}
func (r *metadataPortRepo) SessionsMetadata(context.Context, string) ([]agent.Session, error) {
	r.calls++
	return []agent.Session{}, nil
}
func (r *metadataPortRepo) TaskPayloadDetail(context.Context, int64, int64, int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	r.calls++
	return task.Task{ID: 80}, nil, nil, nil
}

func TestPermissionWrapperPreservesMetadataPort(t *testing.T) {
	source := &metadataPortRepo{}
	wrapped, err := WithPermissions(source, Actor{Role: Agent, Name: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := wrapped.(TaskMetadataRepository)
	if !ok {
		t.Fatal("metadata port missing")
	}
	ctx := context.Background()
	if _, err = reader.TasksMetadata(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.TaskMetadata(ctx, 80); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.SessionsMetadata(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = reader.TaskPayloadDetail(ctx, 80, 0, 50); err != nil {
		t.Fatal(err)
	}
	if source.calls != 4 {
		t.Fatalf("forwarded calls: %d", source.calls)
	}
}
