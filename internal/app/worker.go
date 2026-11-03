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

	"github.com/dominikbb/looster/internal/namespace"
	"github.com/dominikbb/looster/internal/task"
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
	if e := s.Queue.Heartbeat(ctx, c.ID, c.Agents, settings); e != nil {
		return e
	}
	s.Log.Info("worker started", "worker", c.ID, "agents", c.Agents, "concurrency", c.Concurrency)
	s.record(ctx, "worker_started", fmt.Sprintf("Worker %s started; agents=%s; concurrency=%d", c.ID, strings.Join(c.Agents, ","), c.Concurrency))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Queue.Leave(cleanup, c.ID)
		s.record(cleanup, "worker_stopped", fmt.Sprintf("Worker %s stopped", c.ID))
		s.Log.Info("worker stopped", "worker", c.ID)
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
				lease, e := s.Queue.Claim(ctx, c.Agents)
				if e != nil {
					s.Log.Error("claim job", "error", e)
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
				s.Log.Error("dispatch recurring rules", "error", err)
				s.record(ctx, "scheduler_failed", fmt.Sprintf("Worker %s: %v", c.ID, err))
			}
		case <-tick.C:
			if e := s.Queue.Heartbeat(ctx, c.ID, c.Agents, settings); e != nil {
				s.Log.Error("heartbeat", "error", e)
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
	var err error
	defer func() {
		if err != nil {
			// A stopped worker must leave its job resumable even when the
			// subprocess or an I/O operation reports a different shutdown error.
			// RunTimeout uses a child context and remains a genuine failure.
			if errors.Is(ctx.Err(), context.Canceled) && !errors.Is(err, ErrTaskClosed) {
				err = context.Canceled
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := lease.Fail(cleanup, err); e != nil {
				s.Log.Error("record failure", "error", e)
			}
			if errors.Is(err, context.Canceled) {
				s.Log.Info("job interrupted; will resume", "job", j.ID, "agent", j.Agent)
			} else if errors.Is(err, ErrTaskClosed) {
				s.Log.Info("task closed; session stopped", "job", j.ID, "agent", j.Agent)
			} else {
				s.Log.Error("job failed", "job", j.ID, "agent", j.Agent, "error", err)
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
		if err = lease.Started(ctx); err != nil {
			return
		}
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
		result, err = s.Harness.Run(watchCtx, Run{Namespace: c.Namespace, Agent: j.Agent, Task: t, SessionID: j.SessionID, Workdir: workdir, Prompt: prompt, ArchivePrefix: prefix}, func(o Output) error { return lease.Capture(watchCtx, o) })
		cause := context.Cause(watchCtx)
		stopWatch(nil)
		<-watchDone
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
		if err = lease.Archive(ctx, result); err != nil {
			return
		}
		j.SessionID = result.SessionID
		j.History = result.History
	}
	key := fmt.Sprintf("%s/%s.json", prefix, filepath.Base(j.SessionID))
	if err = s.Objects.Put(ctx, key, bytes.NewReader(j.History), int64(len(j.History))); err != nil {
		return
	}
	err = lease.Complete(ctx)
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
		s.Log.Error("persist system event", "kind", kind, "error", err)
	}
}
func (s *Service) schedule(ctx context.Context, c WorkerConfig) (int, error) {
	count, err := s.Queue.Schedule(ctx, c.Agents)
	if err == nil && count > 0 {
		s.record(ctx, "scheduler_dispatched", fmt.Sprintf("Worker %s dispatched %d scheduled ping(s); agents=%s", c.ID, count, strings.Join(c.Agents, ",")))
		s.Log.Info("scheduled pings dispatched", "worker", c.ID, "count", count)
	}
	return count, err
}
