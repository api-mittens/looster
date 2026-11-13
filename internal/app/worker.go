package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/task"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Job struct {
	ID            int64
	TaskID        int64
	Agent         string
	SessionID     string
	State         string
	History       []byte
	Prompt        string
	ContextTaskID int64
	Notification  bool
}

const sessionSummaryInstruction = "\n\nSession reporting requirement: End every run with a 4-5 sentence session summary in your final response, including runs with no task or no changes. Describe what you attempted, concrete outcomes and verification, and any blockers or unfinished work. Explicitly say when no work was performed and why. Do not claim success when execution failed or was blocked.\n"

var ErrTaskClosed = errors.New("task closed")
var ErrTaskPingStopped = errors.New("ping stopped by behaviour")

// A lease owns a connection-scoped lock for an agent/task pair until Close.
type Lease interface {
	Job() Job
	Started(context.Context) error
	Capture(context.Context, Output) error
	Archive(context.Context, Result) error
	Complete(context.Context) error
	Fail(context.Context, error) error
	Close()
}
type Queue interface {
	RecordSystemEvent(context.Context, string, string) error
	Schedule(context.Context, []string) (int, error)
	Claim(context.Context, []string) (Lease, error)
	Heartbeat(context.Context, string, []string, WorkerSettings) error
	Leave(context.Context, string) error
}
type WorkerConfig struct {
	Namespace   string
	Harness     string
	ID          string
	Agents      []string
	Concurrency int
	Workdir     string
	Poll        time.Duration
	RunTimeout  time.Duration
}
type Service struct {
	Repo    Repository
	Queue   Queue
	Harness Harness
	Objects Objects
	Log     *slog.Logger
}

func (s *Service) Serve(ctx context.Context, c WorkerConfig) error {
	if c.Namespace == "" {
		c.Namespace = namespace.Default
	}
	if err := namespace.Validate(c.Namespace); err != nil {
		return err
	}
	if len(c.Agents) == 0 {
		return errors.New("no agents configured: set LOOSTER_AGENTS or agents in the TOML config")
	}
	if c.Concurrency < 1 || c.Poll <= 0 || c.RunTimeout < 0 {
		return errors.New("serve requires positive concurrency and poll interval, and a nonnegative run timeout")
	}
	for _, name := range c.Agents {
		if _, e := s.Repo.Agent(ctx, name); e != nil {
			return fmt.Errorf("configured agent %q: %w", name, e)
		}
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	settings := WorkerSettings{Harness: c.Harness, Concurrency: c.Concurrency, PollInterval: c.Poll.String(), RunTimeout: c.RunTimeout.String()}
	if e := s.heartbeat(ctx, c, settings); e != nil {
		return e
	}
	newWorkerTelemetry().capacity.Record(ctx, int64(c.Concurrency))
	s.Log.InfoContext(ctx, "worker started", "worker", c.ID, "agents", c.Agents, "concurrency", c.Concurrency)
	s.record(ctx, "worker_started", fmt.Sprintf("Worker %s started; agents=%s; concurrency=%d", c.ID, strings.Join(c.Agents, ","), c.Concurrency))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.Queue.Leave(cleanup, c.ID)
		s.record(cleanup, "worker_stopped", fmt.Sprintf("Worker %s stopped", c.ID))
		s.Log.InfoContext(ctx, "worker stopped", "worker", c.ID)
	}()
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	if _, err := s.schedule(ctx, c); err != nil {
		return fmt.Errorf("dispatch recurring rules (run looster db migrate after upgrading): %w", err)
	}
	for i := 0; i < c.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				lease, e := s.claim(ctx, c)
				if e != nil {
					s.Log.ErrorContext(ctx, "Claim failed", "error_class", workerOutcome(e))
					if ctx.Err() == nil {
						s.record(ctx, "worker_error", fmt.Sprintf("Worker %s could not claim a job: %v", c.ID, e))
					}
				}
				if lease != nil {
					s.process(ctx, c, lease)
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(c.Poll):
				}
			}
		}()
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	scheduleTick := time.NewTicker(c.Poll)
	defer scheduleTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-scheduleTick.C:
			if _, err := s.schedule(ctx, c); err != nil && ctx.Err() == nil {
				s.Log.ErrorContext(ctx, "Schedule failed", "error_class", workerOutcome(err))
				s.record(ctx, "scheduler_failed", fmt.Sprintf("Worker %s: %v", c.ID, err))
			}
		case <-tick.C:
			if e := s.heartbeat(ctx, c, settings); e != nil {
				s.Log.ErrorContext(ctx, "Heartbeat failed", "error_class", workerOutcome(e))
				if ctx.Err() == nil {
					s.record(ctx, "worker_error", fmt.Sprintf("Worker %s heartbeat failed: %v", c.ID, e))
				}
			}
		}
	}
}
func (s *Service) process(ctx context.Context, c WorkerConfig, lease Lease) {
	if c.Namespace == "" {
		c.Namespace = namespace.Default
	}
	if c.Namespace != namespace.Default {
		c.Workdir = filepath.Join(c.Workdir, "namespaces", c.Namespace)
	}
	defer lease.Close()
	j := lease.Job()
	ctx, attempt := otel.Tracer(workerScope).Start(ctx, "looster.job.process", trace.WithNewRoot(), trace.WithAttributes(attribute.Int64("job.id", j.ID), attribute.String("agent", j.Agent), attribute.String("namespace", c.Namespace)))
	if j.TaskID > 0 {
		attempt.SetAttributes(attribute.Int64("task.id", j.TaskID))
	}
	telemetry := newWorkerTelemetry()
	started := time.Now()
	telemetry.active.Add(ctx, 1)
	var err error
	defer func() {
		attempt.SetAttributes(attribute.String("session.id", j.SessionID))
		outcome := workerOutcome(err)
		attrs := metric.WithAttributes(attribute.String("outcome", outcome))
		telemetry.attempts.Add(ctx, 1, attrs)
		telemetry.duration.Record(ctx, time.Since(started).Seconds(), attrs)
		telemetry.active.Add(ctx, -1)
		s.Log.InfoContext(ctx, "Job attempt stopped", "job", j.ID, "outcome", outcome)
		workerSpanEnd(attempt, err)
	}()
	s.Log.InfoContext(ctx, "Job attempt started", "job", j.ID, "agent", j.Agent)
	defer func() {
		if err != nil {
			// A stopped worker must leave its job resumable even when the
			// subprocess or an I/O operation reports a different shutdown error.
			// RunTimeout uses a child context and remains a genuine failure.
			if errors.Is(ctx.Err(), context.Canceled) && !errors.Is(err, ErrTaskClosed) {
				err = context.Canceled
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if e := lease.Fail(cleanup, err); e != nil {
				s.Log.ErrorContext(ctx, "Record failure failed", "error_class", "failed")
			}
			if errors.Is(err, context.Canceled) {
				s.Log.InfoContext(ctx, "job interrupted; will resume", "job", j.ID, "agent", j.Agent)
			} else if errors.Is(err, ErrTaskClosed) {
				s.Log.InfoContext(ctx, "task closed; session stopped", "job", j.ID, "agent", j.Agent)
			} else {
				s.Log.ErrorContext(ctx, "Job failed", "job", j.ID, "agent", j.Agent, "error_class", workerOutcome(err))
			}
		}
	}()
	archiveTaskID := j.TaskID
	if archiveTaskID == 0 {
		archiveTaskID = j.ContextTaskID
	}
	prefix := fmt.Sprintf("sessions/%s/scheduled/job-%d", j.Agent, j.ID)
	if archiveTaskID > 0 {
		prefix = fmt.Sprintf("sessions/%s/%s", j.Agent, task.ID(archiveTaskID))
	}
	if c.Namespace != namespace.Default {
		prefix = "namespaces/" + c.Namespace + "/" + prefix
	}
	if j.State != "archiving" {
		prepareCtx, prepare := otel.Tracer(workerScope).Start(ctx, "looster.job.prepare")
		prepareDone := false
		defer func() {
			if !prepareDone {
				workerSpanEnd(prepare, err)
			}
		}()
		jobCtx := ctx
		ctx = prepareCtx
		var t task.Task
		prompt := j.Prompt
		workdir := ""
		if j.TaskID > 0 {
			t, err = s.Repo.Task(ctx, j.TaskID)
			if err != nil {
				return
			}
			if t.Status == task.Closed {
				err = ErrTaskClosed
				return
			}
			prompt, err = s.prompt(ctx, j.Agent, t)
			if err != nil {
				return
			}
			if j.Prompt != "" {
				prompt += "\nInstruction for this run:\n" + j.Prompt + "\n"
			}
			workdir = filepath.Join(c.Workdir, fmt.Sprintf("%s-%s", j.Agent, task.ID(t.ID)))
		} else if j.ContextTaskID > 0 {
			workdir = filepath.Join(c.Workdir, fmt.Sprintf("%s-%s", j.Agent, task.ID(j.ContextTaskID)))
		} else {
			if c.Namespace != namespace.Default {
				workdir = filepath.Join(c.Workdir, "scheduled", fmt.Sprintf("%s-job-%d", j.Agent, j.ID))
			}
			var expanded TaskContext
			expanded, err = ExpandTaskContext(ctx, s.Repo, task.Task{}, j.Agent)
			if err != nil {
				return
			}
			var instructions strings.Builder
			instructions.WriteString(expanded.Agent.Description)
			for _, m := range expanded.Agent.Memories {
				fmt.Fprintf(&instructions, "\n\nAgent memory MEM-%d:\n%s", m.ID, m.Body)
			}
			if instructions.Len() > 0 {
				prompt = instructions.String() + "\n\n" + j.Prompt
			}
		}
		if prompt == "" {
			err = errors.New("job has no prompt")
			return
		}
		prompt += sessionSummaryInstruction + agentOutputLanguageInstruction
		if err = lease.Started(ctx); err != nil {
			return
		}
		workerSpanEnd(prepare, nil)
		prepareDone = true
		ctx = jobCtx
		runCtx := ctx
		cancel := func() {}
		if c.RunTimeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, c.RunTimeout)
		}
		defer cancel()
		watchCtx, stopWatch := context.WithCancelCause(runCtx)
		watchDone := make(chan struct{})
		if j.TaskID > 0 {
			go func() {
				defer close(watchDone)
				interval := c.Poll
				if interval <= 0 || interval > time.Second {
					interval = time.Second
				} else if interval < 250*time.Millisecond {
					interval = 250 * time.Millisecond
				}
				tick := time.NewTicker(interval)
				defer tick.Stop()
				for {
					select {
					case <-watchCtx.Done():
						return
					case <-tick.C:
						taskState, checkErr := s.Repo.Task(watchCtx, j.TaskID)
						if checkErr == nil && taskState.Status == task.Closed {
							stopWatch(ErrTaskClosed)
							return
						}
					}
				}
			}()
		} else {
			close(watchDone)
		}
		var result Result
		harnessCtx, harnessSpan := otel.Tracer(workerScope).Start(watchCtx, "looster.job.execute")
		result, err = s.Harness.Run(harnessCtx, Run{JobID: j.ID, Namespace: c.Namespace, Agent: j.Agent, Task: t, SessionID: j.SessionID, Workdir: workdir, Prompt: prompt, ArchivePrefix: prefix}, func(o Output) error {
			start := time.Now()
			captureErr := lease.Capture(harnessCtx, o)
			telemetry.operation(harnessCtx, "capture", start, captureErr, false)
			return captureErr
		})
		cause := context.Cause(watchCtx)
		stopWatch(nil)
		<-watchDone
		if errors.Is(cause, ErrTaskClosed) {
			err = ErrTaskClosed
		}
		if errors.Is(cause, context.DeadlineExceeded) {
			err = context.DeadlineExceeded
		}
		if err != nil && errors.Is(ctx.Err(), context.Canceled) && !errors.Is(err, ErrTaskClosed) {
			err = context.Canceled
		}
		workerSpanEnd(harnessSpan, err)
		if errors.Is(cause, ErrTaskClosed) {
			err = ErrTaskClosed
			return
		}
		if err != nil {
			return
		}
		if j.TaskID > 0 {
			taskState, checkErr := s.Repo.Task(ctx, j.TaskID)
			if checkErr != nil {
				err = checkErr
				return
			}
			if taskState.Status == task.Closed {
				err = ErrTaskClosed
				return
			}
		}
		if result.SessionID == "" {
			err = errors.New("harness returned no session ID")
			return
		}
		archiveStart := time.Now()
		err = lease.Archive(ctx, result)
		telemetry.operation(ctx, "archive", archiveStart, err, true)
		if err != nil {
			return
		}
		j.SessionID = result.SessionID
		j.History = result.History
	} else {
		recoveryCtx, recovery := otel.Tracer(workerScope).Start(ctx, "looster.job.archive_recovery")
		ctx = recoveryCtx
		defer func() { workerSpanEnd(recovery, err) }()
		s.Log.InfoContext(ctx, "Archive recovery started", "job", j.ID)
	}
	key := fmt.Sprintf("%s/%s.json", prefix, filepath.Base(j.SessionID))
	archiveStart := time.Now()
	err = s.Objects.Put(ctx, key, bytes.NewReader(j.History), int64(len(j.History)))
	telemetry.operation(ctx, "archive", archiveStart, err, true)
	if err != nil {
		return
	}
	completeStart := time.Now()
	err = lease.Complete(ctx)
	telemetry.operation(ctx, "complete", completeStart, err, true)
}
func (s *Service) prompt(ctx context.Context, name string, t task.Task) (string, error) {
	expanded, e := ExpandTaskContext(ctx, s.Repo, t, name)
	if e != nil {
		return "", e
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "You are Looster agent %s.\n%s\n\nTask %s: %s\nStatus: %s\nTags: %s\n%s\n", name, expanded.Agent.Description, task.ID(t.ID), t.Name, t.Status, strings.Join(t.Tags, ", "), t.Description)
	for _, v := range expanded.Agent.Memories {
		fmt.Fprintf(&b, "\nAgent memory MEM-%d:\n%s\n", v.ID, v.Body)
	}
	for _, tag := range expanded.Tags {
		fmt.Fprintf(&b, "\nTag %s instructions:\n%s\n", tag.Name, tag.Description)
		for _, v := range tag.Memories {
			fmt.Fprintf(&b, "\nTag %s memory MEM-%d:\n%s\n", tag.Name, v.ID, v.Body)
		}
	}
	// Include the complete log through paged reads, rather than dropping later comments.
	var after int64
	results := map[string]string{}
	for {
		events, e := s.Repo.Events(ctx, t.ID, after)
		if e != nil {
			return "", e
		}
		if len(events) == 0 {
			break
		}
		for _, v := range events {
			if v.Kind == "comment" {
				fmt.Fprintf(&b, "\nComment %d:\n%s\n", v.ID, v.Body)
			}
			if v.Kind == "agent_output" && v.Agent != "" && v.Agent != name {
				var output struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal([]byte(v.Body), &output) == nil && output.Type == "done" && output.Text != "" {
					results[v.Agent] = output.Text
				}
			}
			after = v.ID
		}
	}
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "\nLatest reported result from agent %s:\n%s\n", name, results[name])
	}
	files, e := s.Repo.Attachments(ctx, t.ID)
	if e != nil {
		return "", e
	}
	for _, f := range files {
		fmt.Fprintf(&b, "\nAttachment %d: %s (use looster task file get %s %d OUTPUT)\n", f.ID, f.Name, task.ID(t.ID), f.ID)
	}
	fmt.Fprintf(&b, "\nUse looster task status %s STATUS to update progress. Valid statuses: planning, todo, validated, closed.\n", task.ID(t.ID))
	return b.String(), nil
}

func (s *Service) record(ctx context.Context, kind, body string) {
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.Queue.RecordSystemEvent(writeCtx, kind, body); err != nil && ctx.Err() == nil {
		s.Log.ErrorContext(ctx, "System event write failed", "kind", kind, "error_class", "failed")
	}
}
func (s *Service) schedule(ctx context.Context, c WorkerConfig) (int, error) {
	start := time.Now()
	count, err := s.Queue.Schedule(ctx, c.Agents)
	telemetry := newWorkerTelemetry()
	telemetry.operation(ctx, "schedule", start, err, count > 0)
	if err == nil {
		telemetry.dispatched.Add(ctx, int64(count))
	}
	if err == nil && count > 0 {
		s.record(ctx, "scheduler_dispatched", fmt.Sprintf("Worker %s dispatched %d scheduled ping(s); agents=%s", c.ID, count, strings.Join(c.Agents, ",")))
		s.Log.InfoContext(ctx, "scheduled pings dispatched", "worker", c.ID, "count", count)
	}
	return count, err
}
