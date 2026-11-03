package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type busyHarness struct{ busy atomic.Bool }

func (h *busyHarness) Run(ctx context.Context, r Run, emit func(Output) error) (Result, error) {
	h.busy.Store(true)
	<-ctx.Done()
	return Result{}, ctx.Err()
}

type schedulingQueue struct {
	h         *busyHarness
	claimed   atomic.Bool
	whileBusy chan struct{}
	lease     *fakeLease
	left      atomic.Bool
	scheduled atomic.Bool
	mu        sync.Mutex
	events    []string
	settings  WorkerSettings
}

func (q *schedulingQueue) Schedule(ctx context.Context, names []string) (int, error) {
	if q.h.busy.Load() {
		select {
		case q.whileBusy <- struct{}{}:
		default:
		}
	}
	if q.scheduled.CompareAndSwap(false, true) {
		return 1, nil
	}
	return 0, nil
}
func (q *schedulingQueue) RecordSystemEvent(_ context.Context, kind, body string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, kind)
	return nil
}
func (q *schedulingQueue) Claim(context.Context, []string) (Lease, error) {
	if q.claimed.CompareAndSwap(false, true) {
		return q.lease, nil
	}
	return nil, nil
}
func (q *schedulingQueue) Heartbeat(_ context.Context, _ string, _ []string, settings WorkerSettings) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settings = settings
	return nil
}
func (q *schedulingQueue) Leave(context.Context, string) error { q.left.Store(true); return nil }

func TestSchedulesContinueWhileWorkerBusy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h := &busyHarness{}
	q := &schedulingQueue{h: h, whileBusy: make(chan struct{}, 1), lease: &fakeLease{job: Job{ID: 1, TaskID: 1, Agent: "coder", State: "pending"}}}
	s := Service{Repo: fakeRepo{}, Queue: q, Harness: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, WorkerConfig{Harness: "jcode", ID: "test", Agents: []string{"coder"}, Concurrency: 1, Workdir: t.TempDir(), Poll: 5 * time.Millisecond})
	}()
	select {
	case <-q.whileBusy:
	case <-ctx.Done():
		t.Fatal("scheduler did not run while the worker was occupied")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not shut down")
	}
	if !q.left.Load() {
		t.Fatal("server heartbeat was not cleaned up")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.settings.Harness != "jcode" || q.settings.Concurrency != 1 || q.settings.PollInterval != "5ms" || q.settings.RunTimeout != "0s" {
		t.Fatalf("worker settings missing from heartbeat: %+v", q.settings)
	}
	if !slices.Equal(q.events, []string{"worker_started", "scheduler_dispatched", "worker_stopped"}) {
		t.Fatalf("worker lifecycle events were not persisted: %v", q.events)
	}
}

type failingScheduleQueue struct {
	schedulingQueue
	calls  atomic.Int32
	cancel context.CancelFunc
}

func (q *failingScheduleQueue) Schedule(context.Context, []string) (int, error) {
	if q.calls.Add(1) == 1 {
		return 0, nil
	}
	return 0, errors.New("scheduler unavailable")
}
func (q *failingScheduleQueue) Claim(context.Context, []string) (Lease, error) { return nil, nil }
func (q *failingScheduleQueue) RecordSystemEvent(ctx context.Context, kind, body string) error {
	if err := q.schedulingQueue.RecordSystemEvent(ctx, kind, body); err != nil {
		return err
	}
	if kind == "scheduler_failed" {
		q.cancel()
	}
	return nil
}
func TestSchedulerFailuresAppearInSystemLogs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	q := &failingScheduleQueue{cancel: cancel}
	s := Service{Repo: fakeRepo{}, Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := s.Serve(ctx, WorkerConfig{ID: "test", Agents: []string{"coder"}, Concurrency: 1, Poll: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !slices.Contains(q.events, "scheduler_failed") {
		t.Fatalf("scheduler error missing from system log: %v", q.events)
	}
}
