package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerNamespaceRuntimeIdentityAndPaths(t *testing.T) {
	for _, name := range []string{"", "default", "research"} {
		for _, scheduled := range []bool{false, true} {
			t.Run(name+"/"+map[bool]string{false: "task", true: "scheduled"}[scheduled], func(t *testing.T) {
				job := Job{ID: 1, TaskID: 1, Agent: "coder", State: "pending"}
				repo := Repository(fakeRepo{})
				if scheduled {
					job.TaskID = 0
					job.Prompt = "Tick"
					repo = agentOnlyRepo{}
				}
				lease := &fakeLease{job: job}
				harness := &fakeHarness{}
				objects := &fakeObjects{calls: &lease.calls}
				service := Service{Repo: repo, Harness: harness, Objects: objects, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
				root := t.TempDir()
				service.process(context.Background(), WorkerConfig{Namespace: name, Workdir: root}, lease)
				if lease.failed != nil {
					t.Fatal(lease.failed)
				}
				selected := name
				if selected == "" {
					selected = "default"
				}
				if harness.run.Namespace != selected {
					t.Fatalf("namespace identity: %+v", harness.run)
				}
				prefix := "sessions/coder/TSK-1"
				workdir := filepath.Join(root, "coder-TSK-1")
				if scheduled {
					prefix = "sessions/coder/scheduled/job-1"
					workdir = ""
				}
				if selected != "default" {
					prefix = "namespaces/" + selected + "/" + prefix
					workdir = filepath.Join(root, "namespaces", selected, "coder-TSK-1")
					if scheduled {
						workdir = filepath.Join(root, "namespaces", selected, "scheduled", "coder-job-1")
					}
				}
				if harness.run.ArchivePrefix != prefix || !strings.HasPrefix(objects.key, prefix+"/") {
					t.Fatalf("archive isolation: run=%+v object=%s", harness.run, objects.key)
				}
				if harness.run.Workdir != workdir {
					t.Fatalf("workdir %q != %q", harness.run.Workdir, workdir)
				}
			})
		}
	}
}

func TestWorkerNamespaceValidationBeforePorts(t *testing.T) {
	err := (&Service{}).Serve(context.Background(), WorkerConfig{Namespace: "../unsafe"})
	if err == nil || !strings.Contains(err.Error(), "invalid namespace") {
		t.Fatalf("invalid namespace reached ports: %v", err)
	}
}
