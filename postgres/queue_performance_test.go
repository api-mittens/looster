package postgres

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dominikbb/looster/app"
	"github.com/dominikbb/looster/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Use the existing disposable namespace helper, never application rows.
func queuePerformanceStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	base, s := manifestTestStore(t)
	ctx, cancel := context.WithTimeout(base, 30*time.Second)
	t.Cleanup(cancel)
	if _, err := s.ApplyManifest(ctx, `AGENT queue_test WITH "Work"; AGENT queue_other WITH "Other";`); err != nil {
		t.Fatal(err)
	}
	return ctx, s
}

func queuePerformanceJob(t *testing.T, ctx context.Context, s *Store, taskID, contextID any, agent, state string, history []byte) int64 {
	t.Helper()
	var id int64
	err := s.Pool.QueryRow(ctx, `WITH event AS (INSERT INTO events(kind,agent_name) VALUES('scheduled',$3) RETURNING id)
 INSERT INTO jobs(task_id,context_task_id,agent_name,event_id,state,history,prompt,session_id,notification)
 SELECT $1,$2,$3,id,$4,$5,'queued prompt','queued-session',true FROM event RETURNING id`, taskID, contextID, agent, state, history).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type queueQueryTrace struct {
	mu      sync.Mutex
	queries []string
}

func (q *queueQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queries = append(q.queries, data.SQL)
	return ctx
}
func (*queueQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestQueueClaimLoadsHistoryOnlyAfterLock(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	history := bytes.Repeat([]byte("archive history\n"), 1<<16)
	first := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "archiving", history)
	second := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "running", history)
	owner, err := s.Claim(ctx, []string{"queue_test"})
	if err != nil || owner == nil {
		t.Fatalf("first claim: %v %v", owner, err)
	}
	defer owner.Close()
	if owner.Job().ID != first {
		t.Fatalf("first claim = %d, want %d", owner.Job().ID, first)
	}
	trace := &queueQueryTrace{}
	cfg := s.Pool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	worker := &Store{Pool: pool}
	claimed, err := worker.Claim(ctx, []string{"queue_test"})
	if err != nil || claimed == nil {
		t.Fatalf("claim past locked candidate: %v %v", claimed, err)
	}
	defer claimed.Close()
	job := claimed.Job()
	if job.ID != second || job.State != "running" || job.SessionID != "queued-session" || job.Prompt != "queued prompt" || !job.Notification || !bytes.Equal(job.History, history) {
		t.Fatalf("claimed job metadata/history mismatch: id=%d state=%s history bytes=%d", job.ID, job.State, len(job.History))
	}
	if !bytes.Equal(owner.Job().History, history) {
		t.Fatal("archiving history was not loaded")
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	candidates, reloads, locks := 0, 0, 0
	for _, query := range trace.queries {
		if strings.Contains(query, "FROM jobs j WHERE") {
			candidates++
			if strings.Contains(strings.Split(query, " FROM ")[0], "history") {
				t.Fatal("candidate query eagerly loads history")
			}
		}
		if strings.Contains(query, "pg_try_advisory_lock") {
			locks++
		}
		if strings.Contains(query, "SELECT state,session_id,history FROM jobs") {
			reloads++
			if locks != 2 {
				t.Fatalf("history loaded before skipping locked candidate: %d lock attempts", locks)
			}
		}
	}
	if candidates != 1 || reloads != 1 {
		t.Fatalf("candidate scans=%d history reloads=%d", candidates, reloads)
	}
}

func TestQueueClaimConcurrentWorkersPreserveFIFO(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "FIFO", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
	second := queuePerformanceJob(t, ctx, s, nil, created.ID, "queue_test", "archiving", []byte("next history"))
	// Another agent on the same task is independent, as are NULL-key schedules.
	other := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_other", "pending", nil)
	standalone := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	cfg := s.Pool.Config()
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	worker := &Store{Pool: pool}
	type result struct {
		lease app.Lease
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 4)
	for range 4 {
		go func() {
			<-start
			l, err := worker.Claim(ctx, []string{"queue_test", "queue_other"})
			results <- result{l, err}
		}()
	}
	close(start)
	seen := map[int64]bool{}
	// Keep every lease held until all workers finish so a duplicate cannot hide.
	var held []app.Lease
	defer func() {
		for _, l := range held {
			l.Close()
		}
	}()
	for range 4 {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent claim: %v", r.err)
			continue
		}
		if r.lease == nil {
			continue
		}
		held = append(held, r.lease)
		id := r.lease.Job().ID
		if seen[id] {
			t.Errorf("duplicate lease for job %d", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 || !seen[first] || !seen[other] || !seen[standalone] || seen[second] {
		t.Fatalf("FIFO/exclusivity violated: claimed %v", seen)
	}
	for _, l := range held {
		l.Close()
	}
	held = nil
	// A delayed predecessor must still block a ready notification successor.
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET available_at=now()+interval '1 hour' WHERE id=$1;`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET state='done' WHERE id=ANY($1)`, []int64{other, standalone}); err != nil {
		t.Fatal(err)
	}
	l, err := s.Claim(ctx, []string{"queue_test"})
	if l != nil {
		l.Close()
		t.Fatal("successor bypassed delayed predecessor")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET state='failed' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	l, err = s.Claim(ctx, []string{"queue_test"})
	if err != nil || l == nil {
		t.Fatalf("claim successor: %v %v", l, err)
	}
	defer l.Close()
	if l.Job().ID != second || l.Job().ContextTaskID != created.ID || string(l.Job().History) != "next history" {
		t.Fatalf("wrong successor: %+v", l.Job())
	}
}

func TestQueueClaimNamespaceLocksRemainIndependent(t *testing.T) {
	ctx, a := queuePerformanceStore(t)
	_, b := queuePerformanceStore(t)
	first := queuePerformanceJob(t, ctx, a, nil, nil, "queue_test", "pending", nil)
	second := queuePerformanceJob(t, ctx, b, nil, nil, "queue_test", "pending", nil)
	if first != second {
		t.Fatalf("expected matching namespace-local IDs: %d != %d", first, second)
	}
	owner, err := a.Claim(ctx, []string{"queue_test"})
	if err != nil || owner == nil {
		t.Fatalf("claim A: %v %v", owner, err)
	}
	defer owner.Close()
	independent, err := b.Claim(ctx, []string{"queue_test"})
	if err != nil || independent == nil {
		t.Fatalf("namespace B blocked by A: %v %v", independent, err)
	}
	defer independent.Close()
}
