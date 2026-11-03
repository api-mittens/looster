package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dominikbb/looster/internal/agent"
	"github.com/dominikbb/looster/internal/event"
	"github.com/dominikbb/looster/internal/memory"
	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

type fakeRepo struct{ Repository }

func (fakeRepo) Task(_ context.Context, id int64) (task.Task, error) {
	return task.Task{ID: id, Name: "Build", Description: "Implement the tool", Tags: []string{"design"}}, nil
}

type closingRepo struct {
	fakeRepo
	closed atomic.Bool
}

func (r *closingRepo) Task(ctx context.Context, id int64) (task.Task, error) {
	t, err := r.fakeRepo.Task(ctx, id)
	if r.closed.Load() {
		t.Status = task.Closed
	}
	return t, err
}

type waitingHarness struct{ started chan struct{} }

func (h waitingHarness) Run(ctx context.Context, _ Run, _ func(Output) error) (Result, error) {
	close(h.started)
	<-ctx.Done()
	return Result{}, ctx.Err()
}

func TestClosedTaskStopsSessions(t *testing.T) {
	repo := &closingRepo{}
	repo.closed.Store(true)
	lease := &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: "pending"}}
	harness := &fakeHarness{}
	s := Service{Repo: repo, Harness: harness, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.process(context.Background(), WorkerConfig{}, lease)
	if !errors.Is(lease.failed, ErrTaskClosed) || harness.calls != 0 {
		t.Fatalf("closed task started a session: failure=%v runs=%d", lease.failed, harness.calls)
	}

	repo.closed.Store(false)
	lease = &fakeLease{job: Job{ID: 2, TaskID: 1, Agent: "coder", State: "running"}}
	started := make(chan struct{})
	s.Harness = waitingHarness{started: started}
	done := make(chan struct{})
	go func() { s.process(context.Background(), WorkerConfig{Poll: 10 * time.Millisecond}, lease); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session did not start")
	}
	repo.closed.Store(true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing did not terminate the session")
	}
	if !errors.Is(lease.failed, ErrTaskClosed) || strings.Join(lease.calls, ",") != "start,fail,close" {
		t.Fatalf("closed session lifecycle: failure=%v calls=%v", lease.failed, lease.calls)
	}
}

func TestScheduledJobsStartFreshSessions(t *testing.T) {
	h := &fakeHarness{}
	var previousPrefix string
	for _, id := range []int64{1, 2} {
		l := &fakeLease{job: Job{ID: id, Agent: "coder", State: "pending", Prompt: "Triage incoming requests."}}
		objects := &fakeObjects{calls: &l.calls}
		// Only agent instructions may be read; no task context is available.
		s := Service{Repo: agentOnlyRepo{}, Harness: h, Objects: objects, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		s.process(context.Background(), WorkerConfig{Workdir: t.TempDir()}, l)
		if l.failed != nil {
			t.Fatal(l.failed)
		}
		if h.run.SessionID != "" || h.run.Task.ID != 0 || h.run.Workdir != "" || h.run.Prompt != "Triage agent instructions.\n\n"+l.job.Prompt {
			t.Fatalf("scheduled ping acquired unwanted context: %+v", h.run)
		}
		if h.run.ArchivePrefix == previousPrefix || !strings.Contains(objects.key, "/scheduled/") {
			t.Fatal("scheduled occurrences did not use distinct archives")
		}
		previousPrefix = h.run.ArchivePrefix
	}
}

func TestTaskNotificationsResumeTheirSession(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		l := &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: "pending", Notification: true, SessionID: "existing_session", Prompt: "Task changed: new comment"}}
		repo := Repository(fakeRepo{})
		if deleted {
			l.job.TaskID = 0
			l.job.ContextTaskID = 1
			l.job.Prompt = "Task was removed; stop work."
			repo = nil
		}
		h := &fakeHarness{}
		objects := &fakeObjects{calls: &l.calls}
		s := Service{Repo: repo, Harness: h, Objects: objects, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		s.process(context.Background(), WorkerConfig{Workdir: t.TempDir()}, l)
		if l.failed != nil {
			t.Fatal(l.failed)
		}
		if h.run.SessionID != "existing_session" || !strings.Contains(h.run.Prompt, l.job.Prompt) {
			t.Fatal("notification did not resume the session with the change")
		}
		if h.run.ArchivePrefix != "sessions/coder/TSK-1" {
			t.Fatal("notification switched archive context")
		}
	}
}

func (fakeRepo) Agent(context.Context, string) (agent.Agent, error) {
	return agent.Agent{Name: "coder", Description: "Write code"}, nil
}
func (fakeRepo) Tag(_ context.Context, name string) (tag.Tag, error) {
	return tag.Tag{Name: name, Description: "Design instructions: match the established system."}, nil
}
func (fakeRepo) Memories(_ context.Context, kind, name string) ([]memory.Memory, error) {
	return []memory.Memory{{ID: 1, Body: kind + " context"}}, nil
}
func (fakeRepo) Events(_ context.Context, id, after int64) ([]event.Event, error) {
	if after > 0 {
		return nil, nil
	}
	return []event.Event{{ID: 1, Kind: "comment", Body: "Important requirement"}}, nil
}
func (fakeRepo) Attachments(context.Context, int64) ([]task.Attachment, error) {
	return []task.Attachment{{ID: 2, Name: "spec.txt"}}, nil
}

type fakeLease struct {
	job    Job
	calls  []string
	failed error
}

func (l *fakeLease) Job() Job                      { return l.job }
func (l *fakeLease) Started(context.Context) error { l.calls = append(l.calls, "start"); return nil }
func (l *fakeLease) Capture(context.Context, Output) error {
	l.calls = append(l.calls, "output")
	return nil
}
func (l *fakeLease) Archive(_ context.Context, r Result) error {
	l.calls = append(l.calls, "archive")
	l.job.State = "archiving"
	return nil
}
func (l *fakeLease) Complete(context.Context) error {
	l.calls = append(l.calls, "complete")
	return nil
}
func (l *fakeLease) Fail(_ context.Context, e error) error {
	l.calls = append(l.calls, "fail")
	l.failed = e
	return nil
}
func (l *fakeLease) Close() { l.calls = append(l.calls, "close") }

type fakeHarness struct {
	run   Run
	calls int
	err   error
}

type interruptedHarness struct {
	cancel context.CancelFunc
}

func (h interruptedHarness) Run(context.Context, Run, func(Output) error) (Result, error) {
	h.cancel()
	return Result{}, errors.New("subprocess killed")
}

func TestWorkerShutdownKeepsInterruptedJobRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: "running", SessionID: "existing_session"}}
	s := Service{Repo: fakeRepo{}, Harness: interruptedHarness{cancel}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.process(ctx, WorkerConfig{}, l)
	if !errors.Is(l.failed, context.Canceled) {
		t.Fatalf("shutdown must requeue rather than permanently fail: %v", l.failed)
	}
	if got := strings.Join(l.calls, ","); got != "start,fail,close" {
		t.Fatalf("shutdown lifecycle: %s", got)
	}
}

func TestWorkerRunTimeoutRemainsFailure(t *testing.T) {
	l := &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: "running"}}
	s := Service{Repo: fakeRepo{}, Harness: &fakeHarness{err: context.DeadlineExceeded}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.process(context.Background(), WorkerConfig{RunTimeout: time.Second}, l)
	if !errors.Is(l.failed, context.DeadlineExceeded) {
		t.Fatalf("timeout must remain a failure: %v", l.failed)
	}
}

func (h *fakeHarness) Run(ctx context.Context, r Run, emit func(Output) error) (Result, error) {
	h.run = r
	h.calls++
	if h.err != nil {
		return Result{}, h.err
	}
	if e := emit(Output{Text: "done"}); e != nil {
		return Result{}, e
	}
	return Result{SessionID: "session_saved", History: []byte(`{"messages":["full history"]}`)}, nil
}

type fakeObjects struct {
	calls     *[]string
	err       error
	body, key string
}

func (o *fakeObjects) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	*o.calls = append(*o.calls, "upload")
	b, e := io.ReadAll(r)
	if e != nil {
		return e
	}
	o.body = string(b)
	o.key = key
	return o.err
}
func (*fakeObjects) Get(context.Context, string) (io.ReadCloser, error) { panic("unused") }
func TestWorkerLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, state       string
		runErr, uploadErr error
		want              string
		runs              int
	}{
		{"fresh", "pending", nil, nil, "start,output,archive,upload,complete,close", 1},
		{"resume", "running", nil, nil, "start,output,archive,upload,complete,close", 1},
		{"archive recovery", "archiving", nil, nil, "upload,complete,close", 0},
		{"harness failure", "pending", errors.New("harness failed"), nil, "start,fail,close", 1},
		{"upload failure", "pending", nil, errors.New("S3 down"), "start,output,archive,upload,fail,close", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: tc.state, SessionID: "session_saved", History: []byte(`{"messages":[]}`)}}
			l.job.Prompt = "Check all open tasks -- report blockers"
			h := &fakeHarness{err: tc.runErr}
			o := &fakeObjects{calls: &l.calls, err: tc.uploadErr}
			s := Service{Repo: fakeRepo{}, Harness: h, Objects: o, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			s.process(context.Background(), WorkerConfig{Workdir: t.TempDir()}, l)
			if got := strings.Join(l.calls, ","); got != tc.want {
				t.Fatalf("calls %s", got)
			}
			if h.calls != tc.runs {
				t.Fatalf("harness calls %d", h.calls)
			}
			if tc.runs > 0 && tc.runErr == nil {
				for _, want := range []string{"Important requirement", "agent context", "tag context", "Design instructions: match the established system.", "spec.txt", l.job.Prompt} {
					if !strings.Contains(h.run.Prompt, want) {
						t.Errorf("prompt missing %s", want)
					}
				}
				if h.run.SessionID != "session_saved" {
					t.Error("did not resume session")
				}
			}
			if tc.uploadErr != nil && l.job.State != "archiving" {
				t.Error("result not durably staged before failed upload")
			}
		})
	}
}

type agentOnlyRepo struct{ Repository }

func (agentOnlyRepo) Agent(_ context.Context, name string) (agent.Agent, error) {
	return agent.Agent{Name: name, Description: "Triage agent instructions."}, nil
}
func (agentOnlyRepo) Memories(context.Context, string, string) ([]memory.Memory, error) {
	return nil, nil
}

func TestScheduledAgentReceivesMemories(t *testing.T) {
	l := &fakeLease{job: Job{ID: 1, Agent: "coder", State: "pending", Prompt: "Triage work"}}
	h := &fakeHarness{}
	s := Service{Repo: fakeRepo{}, Harness: h, Objects: &fakeObjects{calls: &l.calls}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.process(context.Background(), WorkerConfig{}, l)
	if l.failed != nil {
		t.Fatal(l.failed)
	}
	for _, want := range []string{"Write code", "agent context", "Triage work"} {
		if !strings.Contains(h.run.Prompt, want) {
			t.Fatalf("scheduled prompt missing %q", want)
		}
	}
}

type handoffRepo struct {
	fakeRepo
	requested int64
}

func (r *handoffRepo) Task(_ context.Context, id int64) (task.Task, error) {
	r.requested = id
	return task.Task{ID: id, Name: "Settings", Description: "Implement settings for repository X", Status: task.Todo, Tags: []string{"design"}}, nil
}
func (r *handoffRepo) Agent(_ context.Context, name string) (agent.Agent, error) {
	return agent.Agent{Name: name, Description: "Review the actual implementation and tests."}, nil
}
func (r *handoffRepo) Events(_ context.Context, id, after int64) ([]event.Event, error) {
	if after > 0 {
		return nil, nil
	}
	return []event.Event{
		{ID: 1, Kind: "comment", Agent: "coder", Body: "Branch feature/settings, commit abc123."},
		{ID: 2, Kind: "agent_output", Agent: "coder", Body: `{"type":"done","text":"Old result"}`},
		{ID: 3, Kind: "agent_output", Agent: "coder", Body: `{"type":"done","text":"Implemented settings; 12 tests passed."}`},
	}, nil
}
func TestAfterHandoffIncludesConcreteTaskContext(t *testing.T) {
	for _, target := range []string{"reviewer", "analyst"} {
		for _, resume := range []bool{false, true} {
			session := ""
			if resume {
				session = target + "_existing"
			}
			t.Run(target+"/session="+session, func(t *testing.T) {
				repo := &handoffRepo{}
				h := &fakeHarness{}
				l := &fakeLease{job: Job{ID: 7, TaskID: 42, Agent: target, State: "pending", SessionID: session, Prompt: "AFTER agent coder completed on task TSK-42. Source session: coder_session"}}
				objects := &fakeObjects{calls: &l.calls}
				s := Service{Repo: repo, Harness: h, Objects: objects, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
				s.process(context.Background(), WorkerConfig{Workdir: t.TempDir()}, l)
				if l.failed != nil {
					t.Fatal(l.failed)
				}
				if repo.requested != 42 || h.run.Task.ID != 42 || h.run.SessionID != session {
					t.Fatalf("handoff lost its task or used source agent's session: %+v", h.run)
				}
				for _, want := range []string{"TSK-42", "repository X", "todo", "Tags: design", "Review the actual implementation", "agent context", "tag context", "Branch feature/settings", "12 tests passed", "coder_session", "spec.txt"} {
					if !strings.Contains(h.run.Prompt, want) {
						t.Errorf("missing handoff context %q", want)
					}
				}
				if strings.Contains(h.run.Prompt, "Old result") {
					t.Error("included outdated peer result")
				}
				if !strings.Contains(h.run.Workdir, target+"-TSK-42") || !strings.Contains(objects.key, "/TSK-42/") {
					t.Error("handoff context did not remain task-scoped")
				}
			})
		}
	}
}
