package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/dominikbb/looster/agent"
	"github.com/dominikbb/looster/behaviour"
	"github.com/dominikbb/looster/event"
	"github.com/dominikbb/looster/task"
)

type Role string

const (
	Human Role = "human"
	Agent Role = "agent"
)

var ErrForbidden = errors.New("permission denied")

type Actor struct {
	Role Role
	Name string
}

func NewActor(role, name string) (Actor, error) {
	a := Actor{Role: Role(role), Name: name}
	switch a.Role {
	case Human:
		return a, nil
	case Agent:
		if agent.ValidName(name) {
			return a, nil
		}
		return Actor{}, errors.New("agent mode requires a valid LOOSTER_AGENT or --agent-name")
	}
	return Actor{}, fmt.Errorf("invalid role %q: use human or agent", role)
}
func (a Actor) RequireHuman() error {
	if a.Role != Human {
		return fmt.Errorf("%w: this action requires the human role", ErrForbidden)
	}
	return nil
}

// WithPermissions is the application's authorization boundary. Reads and task
// operations are shared; administrative writes and agent-owned data are guarded.
func WithPermissions(repo Repository, actor Actor) (Repository, error) {
	a, err := NewActor(string(actor.Role), actor.Name)
	if err != nil {
		return nil, err
	}
	return &accessRepository{Repository: repo, actor: a}, nil
}

type accessRepository struct {
	Repository
	actor Actor
}

func (r *accessRepository) ApplyManifest(ctx context.Context, source string) (behaviour.Manifest, error) {
	if err := r.actor.RequireHuman(); err != nil {
		return behaviour.Manifest{}, err
	}
	repo, ok := r.Repository.(ManifestRepository)
	if !ok {
		return behaviour.Manifest{}, errors.New("repository does not support versioned manifests")
	}
	return repo.ApplyManifest(ctx, source)
}
func (r *accessRepository) Manifests(ctx context.Context) ([]behaviour.Manifest, error) {
	repo, ok := r.Repository.(ManifestRepository)
	if !ok {
		return nil, errors.New("repository does not support versioned manifests")
	}
	return repo.Manifests(ctx)
}
func (r *accessRepository) ActivateManifest(ctx context.Context, id int64) error {
	if err := r.actor.RequireHuman(); err != nil {
		return err
	}
	repo, ok := r.Repository.(ManifestRepository)
	if !ok {
		return errors.New("repository does not support versioned manifests")
	}
	return repo.ActivateManifest(ctx, id)
}

func (r *accessRepository) context(ctx context.Context) context.Context {
	name := ""
	if r.actor.Role == Agent {
		name = r.actor.Name
	}
	return event.WithActor(ctx, name)
}
func (r *accessRepository) CreateTask(ctx context.Context, name, description string, status task.Status, tags []string) (task.Task, error) {
	return r.Repository.CreateTask(r.context(ctx), name, description, status, tags)
}
func (r *accessRepository) RemoveTask(ctx context.Context, id int64) error {
	return r.Repository.RemoveTask(r.context(ctx), id)
}
func (r *accessRepository) SetStatus(ctx context.Context, id int64, status task.Status) error {
	return r.Repository.SetStatus(r.context(ctx), id, status)
}
func (r *accessRepository) Comment(ctx context.Context, id int64, body string) (int64, error) {
	return r.Repository.Comment(r.context(ctx), id, body)
}
func (r *accessRepository) AddAttachment(ctx context.Context, a task.Attachment) (task.Attachment, error) {
	return r.Repository.AddAttachment(r.context(ctx), a)
}

func (r *accessRepository) CreateTag(ctx context.Context, name, description string) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.CreateTag(ctx, name, description)
}
func (r *accessRepository) RemoveTag(ctx context.Context, name string) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.RemoveTag(ctx, name)
}
func (r *accessRepository) CreateAgent(ctx context.Context, name, description string) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.CreateAgent(ctx, name, description)
}
func (r *accessRepository) RemoveAgent(ctx context.Context, name string) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.RemoveAgent(ctx, name)
}
func (r *accessRepository) CreateBehaviour(ctx context.Context, rules []behaviour.Rule) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.CreateBehaviour(ctx, rules)
}
func (r *accessRepository) SetBehaviour(ctx context.Context, rules []behaviour.Rule) error {
	if e := r.actor.RequireHuman(); e != nil {
		return e
	}
	return r.Repository.SetBehaviour(ctx, rules)
}
func (r *accessRepository) memory(kind, name string) error {
	if kind != "tag" && kind != "agent" {
		return errors.New("invalid memory owner kind")
	}
	if r.actor.Role == Agent && kind == "agent" && name != r.actor.Name {
		return fmt.Errorf("%w: agents may modify only their own agent memories", ErrForbidden)
	}
	return nil
}
func (r *accessRepository) AddMemory(ctx context.Context, kind, name, body string, wipe bool) error {
	if e := r.memory(kind, name); e != nil {
		return e
	}
	return r.Repository.AddMemory(ctx, kind, name, body, wipe)
}
func (r *accessRepository) RemoveMemory(ctx context.Context, kind, name string, id int64) error {
	if e := r.memory(kind, name); e != nil {
		return e
	}
	return r.Repository.RemoveMemory(ctx, kind, name, id)
}
func (r *accessRepository) Bind(ctx context.Context, id int64, name, sid string) error {
	if r.actor.Role == Agent && name != r.actor.Name {
		return fmt.Errorf("%w: agents may bind only their own sessions", ErrForbidden)
	}
	return r.Repository.Bind(ctx, id, name, sid)
}
