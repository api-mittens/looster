package app

import (
	"context"
	"fmt"

	"github.com/dominikbb/looster/internal/agent"
	"github.com/dominikbb/looster/internal/memory"
	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

type ContextRepository interface {
	Agent(context.Context, string) (agent.Agent, error)
	Tag(context.Context, string) (tag.Tag, error)
	Memories(context.Context, string, string) ([]memory.Memory, error)
}
type AgentContext struct {
	agent.Agent
	Memories []memory.Memory `json:"memories"`
}
type TagContext struct {
	tag.Tag
	Memories []memory.Memory `json:"memories"`
}
type TaskContext struct {
	Agent *AgentContext `json:"agent,omitempty"`
	Tags  []TagContext  `json:"tags"`
}

// ExpandTaskContext is shared by task inspection and worker prompt construction.
// Missing context is an error rather than silently running without instructions.
func ExpandTaskContext(ctx context.Context, repo ContextRepository, t task.Task, name string) (TaskContext, error) {
	out := TaskContext{Tags: []TagContext{}}
	if name != "" {
		a, err := repo.Agent(ctx, name)
		if err != nil {
			return out, fmt.Errorf("agent %s context: %w", name, err)
		}
		memories, err := repo.Memories(ctx, "agent", name)
		if err != nil {
			return out, err
		}
		if memories == nil {
			memories = []memory.Memory{}
		}
		out.Agent = &AgentContext{Agent: a, Memories: memories}
	}
	seen := map[string]bool{}
	for _, name := range t.Tags {
		if seen[name] {
			continue
		}
		seen[name] = true
		value, err := repo.Tag(ctx, name)
		if err != nil {
			return out, fmt.Errorf("tag %s context: %w", name, err)
		}
		if value.Inactive {
			continue
		}
		memories, err := repo.Memories(ctx, "tag", name)
		if err != nil {
			return out, err
		}
		if memories == nil {
			memories = []memory.Memory{}
		}
		out.Tags = append(out.Tags, TagContext{Tag: value, Memories: memories})
	}
	return out, nil
}
