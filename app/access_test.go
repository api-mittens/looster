package app

import (
	"context"
	"errors"
	"testing"

	"github.com/dominikbb/looster/behaviour"
	"github.com/dominikbb/looster/event"
	"github.com/dominikbb/looster/task"
)

type permissionRepo struct {
	fakeRepo
	calls int
	actor string
}

func (r *permissionRepo) CreateTag(context.Context, string, string) error      { r.calls++; return nil }
func (r *permissionRepo) RemoveTag(context.Context, string) error              { r.calls++; return nil }
func (r *permissionRepo) CreateAgent(context.Context, string, string) error    { r.calls++; return nil }
func (r *permissionRepo) RemoveAgent(context.Context, string) error            { r.calls++; return nil }
func (r *permissionRepo) SetBehaviour(context.Context, []behaviour.Rule) error { r.calls++; return nil }
func (r *permissionRepo) CreateBehaviour(context.Context, []behaviour.Rule) error {
	r.calls++
	return nil
}
func (r *permissionRepo) AddMemory(context.Context, string, string, string, bool) error {
	r.calls++
	return nil
}
func (r *permissionRepo) RemoveMemory(context.Context, string, string, int64) error {
	r.calls++
	return nil
}
func (r *permissionRepo) Bind(context.Context, int64, string, string) error { r.calls++; return nil }
func (r *permissionRepo) record(ctx context.Context)                        { r.calls++; r.actor = event.Actor(ctx) }
func (r *permissionRepo) SetStatus(ctx context.Context, _ int64, _ task.Status) error {
	r.record(ctx)
	return nil
}
func (r *permissionRepo) CreateTask(ctx context.Context, _ string, _ string, _ task.Status, _ []string) (task.Task, error) {
	r.record(ctx)
	return task.Task{ID: 1}, nil
}
func (r *permissionRepo) RemoveTask(ctx context.Context, _ int64) error { r.record(ctx); return nil }
func (r *permissionRepo) Comment(ctx context.Context, _ int64, _ string) (int64, error) {
	r.record(ctx)
	return 1, nil
}
func (r *permissionRepo) AddAttachment(ctx context.Context, a task.Attachment) (task.Attachment, error) {
	r.record(ctx)
	return a, nil
}

func TestApplicationPermissions(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name         string
		agentAllowed bool
		run          func(Repository) error
	}{
		{"create tag", false, func(r Repository) error { return r.CreateTag(ctx, "design", "") }},
		{"remove tag", false, func(r Repository) error { return r.RemoveTag(ctx, "design") }},
		{"create agent", false, func(r Repository) error { return r.CreateAgent(ctx, "other", "") }},
		{"remove agent", false, func(r Repository) error { return r.RemoveAgent(ctx, "other") }},
		{"replace behaviour", false, func(r Repository) error { return r.SetBehaviour(ctx, nil) }},
		{"create behaviour", false, func(r Repository) error { return r.CreateBehaviour(ctx, nil) }},
		{"add own memory", true, func(r Repository) error { return r.AddMemory(ctx, "agent", "coder", "context", false) }},
		{"wipe own memory", true, func(r Repository) error { return r.AddMemory(ctx, "agent", "coder", "", true) }},
		{"remove own memory", true, func(r Repository) error { return r.RemoveMemory(ctx, "agent", "coder", 1) }},
		{"add foreign memory", false, func(r Repository) error { return r.AddMemory(ctx, "agent", "other", "context", false) }},
		{"wipe foreign memory", false, func(r Repository) error { return r.AddMemory(ctx, "agent", "other", "", true) }},
		{"remove foreign memory", false, func(r Repository) error { return r.RemoveMemory(ctx, "agent", "other", 1) }},
		{"add tag memory", true, func(r Repository) error { return r.AddMemory(ctx, "tag", "design", "context", false) }},
		{"wipe tag memory", true, func(r Repository) error { return r.AddMemory(ctx, "tag", "design", "", true) }},
		{"remove tag memory", true, func(r Repository) error { return r.RemoveMemory(ctx, "tag", "design", 1) }},
		{"bind own session", true, func(r Repository) error { return r.Bind(ctx, 1, "coder", "session") }},
		{"bind foreign session", false, func(r Repository) error { return r.Bind(ctx, 1, "other", "session") }},
		{"set task status", true, func(r Repository) error { return r.SetStatus(ctx, 1, task.Todo) }},
		{"create task", true, func(r Repository) error { _, e := r.CreateTask(ctx, "task", "", task.Planning, nil); return e }},
		{"remove task", true, func(r Repository) error { return r.RemoveTask(ctx, 1) }},
		{"comment", true, func(r Repository) error { _, e := r.Comment(ctx, 1, "comment"); return e }},
		{"read foreign memories", true, func(r Repository) error { _, e := r.Memories(ctx, "agent", "other"); return e }},
	} {
		for _, role := range []Role{Human, Agent} {
			t.Run(tc.name+"/"+string(role), func(t *testing.T) {
				underlying := &permissionRepo{}
				repo, err := WithPermissions(underlying, Actor{Role: role, Name: "coder"})
				if err != nil {
					t.Fatal(err)
				}
				err = tc.run(repo)
				allowed := role == Human || tc.agentAllowed
				if allowed && err != nil {
					t.Fatal(err)
				}
				if !allowed {
					if !errors.Is(err, ErrForbidden) {
						t.Fatalf("expected forbidden, got %v", err)
					}
					if underlying.calls != 0 {
						t.Fatal("denied operation reached persistence")
					}
				}
			})
		}
	}
	for _, actor := range []Actor{{}, {Role: "admin"}, {Role: Agent}, {Role: Agent, Name: "../coder"}} {
		if _, err := WithPermissions(&permissionRepo{}, actor); err == nil {
			t.Fatalf("accepted invalid actor %+v", actor)
		}
	}
}

func TestTaskWritesCarryTheirOriginatingAgent(t *testing.T) {
	ctx := event.WithActor(context.Background(), "untrusted-prior-value")
	for _, tc := range []struct {
		name string
		run  func(Repository) error
	}{
		{"create", func(r Repository) error { _, e := r.CreateTask(ctx, "task", "", task.Planning, nil); return e }},
		{"remove", func(r Repository) error { return r.RemoveTask(ctx, 1) }},
		{"status", func(r Repository) error { return r.SetStatus(ctx, 1, task.Todo) }},
		{"comment", func(r Repository) error { _, e := r.Comment(ctx, 1, "change"); return e }},
		{"attachment", func(r Repository) error { _, e := r.AddAttachment(ctx, task.Attachment{TaskID: 1}); return e }},
	} {
		for _, role := range []Role{Human, Agent} {
			t.Run(tc.name+"/"+string(role), func(t *testing.T) {
				underlying := &permissionRepo{}
				repo, e := WithPermissions(underlying, Actor{Role: role, Name: "coder"})
				if e != nil {
					t.Fatal(e)
				}
				if e = tc.run(repo); e != nil {
					t.Fatal(e)
				}
				want := ""
				if role == Agent {
					want = "coder"
				}
				if underlying.calls != 1 || underlying.actor != want {
					t.Fatalf("incorrect attribution: calls=%d actor=%q", underlying.calls, underlying.actor)
				}
			})
		}
	}
}
