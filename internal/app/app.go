// Package app defines the ports used by the command and worker applications.
package app

import (
	"context"
	"io"

	"github.com/dominikbb/looster/internal/agent"
	"github.com/dominikbb/looster/internal/behaviour"
	"github.com/dominikbb/looster/internal/event"
	"github.com/dominikbb/looster/internal/memory"
	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

type Repository interface {
	Tasks(context.Context) ([]task.Task, error)
	Task(context.Context, int64) (task.Task, error)
	CreateTask(context.Context, string, string, task.Status, []string) (task.Task, error)
	RemoveTask(context.Context, int64) error
	SetStatus(context.Context, int64, task.Status) error
	Comment(context.Context, int64, string) (int64, error)
	Events(context.Context, int64, int64) ([]event.Event, error)
	Logs(context.Context, event.Filter) ([]event.Event, error)
	Tags(context.Context) ([]tag.Tag, error)
	Tag(context.Context, string) (tag.Tag, error)
	CreateTag(context.Context, string, string) error
	RemoveTag(context.Context, string) error
	Agents(context.Context) ([]agent.Agent, error)
	Agent(context.Context, string) (agent.Agent, error)
	CreateAgent(context.Context, string, string) error
	RemoveAgent(context.Context, string) error
	Memories(context.Context, string, string) ([]memory.Memory, error)
	AddMemory(context.Context, string, string, string, bool) error
	RemoveMemory(context.Context, string, string, int64) error
	Sessions(context.Context, string) ([]agent.Session, error)
	Bind(context.Context, int64, string, string) error
	CreateBehaviour(context.Context, []behaviour.Rule) error
	SetBehaviour(context.Context, []behaviour.Rule) error
	Behaviour(context.Context) ([]behaviour.Rule, error)
	AddAttachment(context.Context, task.Attachment) (task.Attachment, error)
	Attachments(context.Context, int64) ([]task.Attachment, error)
	Workers(context.Context) ([]Worker, error)
}
type Worker struct {
	ID       string          `json:"id"`
	Agents   []string        `json:"agents"`
	LastSeen string          `json:"last_seen"`
	Settings *WorkerSettings `json:"settings,omitempty"`
}

// WorkerSettings deliberately excludes credentials, paths and environment values.
type WorkerSettings struct {
	Harness      string `json:"harness"`
	Concurrency  int    `json:"concurrency"`
	PollInterval string `json:"poll_interval"`
	RunTimeout   string `json:"run_timeout"`
}
type Objects interface {
	Put(context.Context, string, io.Reader, int64) error
	Get(context.Context, string) (io.ReadCloser, error)
}
type Run struct {
	Namespace     string
	Agent         string
	Task          task.Task
	SessionID     string
	Workdir       string
	Prompt        string
	ArchivePrefix string
}
type Output struct {
	Text      string
	SessionID string
}
type Result struct {
	SessionID string
	History   []byte
}
type Harness interface {
	Run(context.Context, Run, func(Output) error) (Result, error)
}
