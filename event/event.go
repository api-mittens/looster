package event

import (
	"context"
	"time"
)

type Event struct {
	ID         int64     `json:"id"`
	TaskID     *int64    `json:"task_id,omitempty"`
	ActorAgent string    `json:"actor_agent,omitempty"`
	Kind       string    `json:"kind"`
	Agent      string    `json:"agent,omitempty"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type actorKey struct{}

// WithActor carries the originating agent through application writes.
func WithActor(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, actorKey{}, agent)
}
func Actor(ctx context.Context) string { name, _ := ctx.Value(actorKey{}).(string); return name }

// Filter bounds a log page. Until is an inclusive event ID high-water mark.
type Filter struct {
	TaskID int64
	Agent  string
	Kinds  []string
	After  int64
	Until  int64
	Since  *time.Time
	Limit  int
	Desc   bool
}
