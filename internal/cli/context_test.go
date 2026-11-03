package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/memory"
	"github.com/api-mittens/looster/internal/tag"
	"github.com/api-mittens/looster/internal/task"
	"github.com/spf13/viper"
)

type contextRepo struct{ app.Repository }

func (contextRepo) Task(context.Context, int64) (task.Task, error) {
	return task.Task{ID: 1, Tags: []string{"design"}}, nil
}
func (contextRepo) Agent(context.Context, string) (agent.Agent, error) {
	return agent.Agent{Name: "coder", Description: "Implement carefully"}, nil
}
func (contextRepo) Tag(context.Context, string) (tag.Tag, error) {
	return tag.Tag{Name: "design", Description: "Follow the design system"}, nil
}
func (contextRepo) Memories(_ context.Context, kind, name string) ([]memory.Memory, error) {
	return []memory.Memory{{ID: 1, Body: kind + " instructions from memory"}}, nil
}
func TestTaskInspectionExpandsContext(t *testing.T) {
	a := &application{v: viper.New(), repo: contextRepo{}}
	root := a.command()
	root.PersistentPreRunE = nil
	a.v.Set("agent", "coder")
	root.SetArgs([]string{"task", "TSK-1"})
	var output bytes.Buffer
	root.SetOut(&output)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Ref     string          `json:"ref"`
		Context app.TaskContext `json:"context"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Ref != "TSK-1" || result.Context.Agent == nil || result.Context.Agent.Description != "Implement carefully" {
		t.Fatal("agent instructions missing")
	}
	if len(result.Context.Tags) != 1 || result.Context.Tags[0].Description != "Follow the design system" || len(result.Context.Tags[0].Memories) != 1 {
		t.Fatal("tag context missing")
	}
}
