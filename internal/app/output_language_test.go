package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestOutputLanguageOnEveryDispatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
	}{
		{"task", Job{ID: 1, TaskID: 1, Agent: "coder", State: "pending"}},
		{"resumed task", Job{ID: 2, TaskID: 1, Agent: "reviewer", State: "pending", SessionID: "existing"}},
		{"scheduled", Job{ID: 3, Agent: "triage", State: "pending", Prompt: "Check requests."}},
		{"retry", Job{ID: 4, Agent: "coder", State: "running", SessionID: "retry", Prompt: "Continue work."}},
		{"notification", Job{ID: 5, TaskID: 1, Agent: "coder", State: "pending", Notification: true, SessionID: "existing", Prompt: "New comment."}},
		{"removed task", Job{ID: 6, ContextTaskID: 1, Agent: "coder", State: "pending", Notification: true, SessionID: "existing", Prompt: "Task removed. Stop work."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease := &fakeLease{job: tc.job}
			harness := &fakeHarness{}
			service := Service{Repo: fakeRepo{}, Harness: harness, Objects: &fakeObjects{calls: &lease.calls}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			service.process(context.Background(), WorkerConfig{Workdir: t.TempDir()}, lease)
			if lease.failed != nil {
				t.Fatal(lease.failed)
			}
			if harness.calls != 1 || !strings.HasSuffix(harness.run.Prompt, agentOutputLanguageInstruction) || strings.Count(harness.run.Prompt, agentOutputLanguageInstruction) != 1 {
				t.Fatalf("dispatch must include the language rule once at the end: %+v", harness.run)
			}
		})
	}
}

func TestOutputLanguageScope(t *testing.T) {
	for _, want := range []string{"ASD-STE100 Simplified Technical English", "tasks", "comments", "logs", "session summaries", "shared memories", "documentation", "code comments", "commit messages", "pull request (PR) titles and descriptions", "release notes", "all other artifacts", "other agents", "raw tool output", "Do not claim"} {
		if !strings.Contains(agentOutputLanguageInstruction, want) {
			t.Errorf("language rule must cover %q", want)
		}
	}
}
