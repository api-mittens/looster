package app

import (
	"context"
	"errors"
	"testing"

	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

func TestExpandedTaskContext(t *testing.T) {
	expanded, err := ExpandTaskContext(context.Background(), fakeRepo{}, task.Task{Tags: []string{"design", "design"}}, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Agent == nil || expanded.Agent.Description != "Write code" || len(expanded.Agent.Memories) != 1 {
		t.Fatal("agent instructions/memories were not expanded")
	}
	if len(expanded.Tags) != 1 || expanded.Tags[0].Description != "Design instructions: match the established system." || len(expanded.Tags[0].Memories) != 1 {
		t.Fatal("tag instructions/memories were not expanded")
	}
	expanded, err = ExpandTaskContext(context.Background(), fakeRepo{}, task.Task{Tags: []string{"design"}}, "")
	if err != nil || expanded.Agent != nil || len(expanded.Tags) != 1 {
		t.Fatal("human inspection should expand tag context without choosing an agent")
	}
}

type missingTagRepo struct{ fakeRepo }

func (missingTagRepo) Tag(context.Context, string) (tag.Tag, error) {
	return tag.Tag{}, errors.New("missing tag")
}
func TestMissingTagContextFailsClosed(t *testing.T) {
	if _, err := ExpandTaskContext(context.Background(), missingTagRepo{}, task.Task{Tags: []string{"design"}}, "coder"); err == nil {
		t.Fatal("silently omitted required tag context")
	}
}
