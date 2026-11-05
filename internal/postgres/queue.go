package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

var _ app.Queue = (*Store)(nil)

type lease struct {
	conn *pgxpool.Conn
	job  app.Job
	lock string
}

func (l *lease) Job() app.Job { return l.job }
func (s *Store) Heartbeat(ctx context.Context, id string, agents []string, settings app.WorkerSettings) error {
	// Keep worker liveness independent of the optional settings migration.
	// A server can continue processing jobs while migration 007 is pending.
	_, e := s.Pool.Exec(ctx, `INSERT INTO workers(id,agents) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET agents=excluded.agents,last_seen=now()`, id, agents)
	if e != nil {
		return e
	}
	data, e := json.Marshal(settings)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, `UPDATE workers SET settings=$2 WHERE id=$1`, id, data)
	var pgErr *pgconn.PgError
	if errors.As(e, &pgErr) && pgErr.Code == "42703" {
		return nil
	}
	return e
}
func (s *Store) Leave(ctx context.Context, id string) error {
	_, e := s.Pool.Exec(ctx, `DELETE FROM workers WHERE id=$1`, id)
	return e
}
func (s *Store) Schedule(ctx context.Context, agents []string) (int, error) {
	n, err := s.q.ScheduleDue(ctx, agents)
	return int(n), err
}
func (s *Store) Claim(ctx context.Context, agents []string) (app.Lease, error) {
	c, e := s.Pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	keep := false
	defer func() {
		if !keep {
			c.Release()
		}
	}()
	// Candidate scans must not fetch history: it can be large and only the
	// job whose advisory lock we acquire needs it. Keep unavailable predecessors
	// in the anti-join so delayed retries still preserve per-session FIFO.
	rows, e := c.Query(ctx, `SELECT j.id,coalesce(j.task_id,0),j.agent_name,j.state,j.session_id,j.prompt,coalesce(j.context_task_id,0),j.notification FROM jobs j WHERE j.agent_name=ANY($1) AND j.available_at<=now() AND j.state IN ('pending','running','archiving') AND NOT EXISTS(SELECT FROM jobs earlier WHERE coalesce(earlier.task_id,earlier.context_task_id)=coalesce(j.task_id,j.context_task_id) AND earlier.agent_name=j.agent_name AND earlier.id<j.id AND earlier.state IN ('pending','running','archiving')) ORDER BY j.id LIMIT 100`, agents)
	if e != nil {
		return nil, e
	}
	jobs := []app.Job{}
	for rows.Next() {
		var j app.Job
		if e = rows.Scan(&j.ID, &j.TaskID, &j.Agent, &j.State, &j.SessionID, &j.Prompt, &j.ContextTaskID, &j.Notification); e != nil {
			rows.Close()
			return nil, e
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	// Preserve historical public lock identities, isolate all other schemas.
	var schema string
	if len(jobs) > 0 {
		if e = c.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
			return nil, e
		}
	}
	for _, j := range jobs {
		lockTask := j.TaskID
		if lockTask == 0 {
			lockTask = j.ContextTaskID
		}
		key := fmt.Sprintf("looster:job:%d", j.ID)
		if lockTask > 0 {
			key = fmt.Sprintf("looster:session:%d:%s", lockTask, j.Agent)
		}
		if schema != "public" {
			key = schema + ":" + key
		}
		var locked bool
		if e = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); e != nil {
			return nil, e
		}
		if !locked {
			continue
		}
		// Reload after taking the lock: the previous owner may have captured
		// a new session or staged an archive since the candidate query.
		e = c.QueryRow(ctx, `SELECT state,session_id,history FROM jobs WHERE id=$1 AND available_at<=now()`, j.ID).Scan(&j.State, &j.SessionID, &j.History)
		if e != nil || j.State == "done" || j.State == "failed" {
			if _, unlockErr := c.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); unlockErr != nil {
				c.Hijack().Close(context.Background())
				keep = true
				return nil, unlockErr
			}
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return nil, e
			}
			continue
		}
		if j.State != "archiving" && j.TaskID > 0 && j.SessionID == "" {
			e = c.QueryRow(ctx, `SELECT coalesce((SELECT session_id FROM sessions WHERE task_id=$1 AND agent_name=$2),'')`, j.TaskID, j.Agent).Scan(&j.SessionID)
			if e != nil {
				c.Hijack().Close(context.Background())
				keep = true
				return nil, e
			}
		}
		keep = true
		return &lease{conn: c, job: j, lock: key}, nil
	}
	return nil, nil
}
func (l *lease) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e := l.conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, l.lock); e != nil {
		l.conn.Hijack().Close(ctx)
		return
	}
	l.conn.Release()
}
func (l *lease) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := l.conn.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// eventTask preserves deleted-task audit references, but schedules have none.
func (l *lease) eventTask() any {
	if l.job.TaskID > 0 {
		return l.job.TaskID
	}
	if l.job.ContextTaskID > 0 {
		return l.job.ContextTaskID
	}
	return nil
}
func (l *lease) Started(ctx context.Context) error {
	return l.transaction(ctx, func(tx pgx.Tx) error {
		if l.job.TaskID > 0 {
			var status task.Status
			var pingStopped bool
			if e := tx.QueryRow(ctx, `SELECT t.status,looster_ping_stopped(t.id,j.agent_name)
 FROM tasks t JOIN jobs j ON j.task_id=t.id
 WHERE t.id=$1 AND j.id=$2 FOR UPDATE OF t`, l.job.TaskID, l.job.ID).Scan(&status, &pingStopped); e != nil {
				return e
			}
			if status == task.Closed {
				return app.ErrTaskClosed
			}
			if pingStopped {
				return app.ErrTaskPingStopped
			}
		}
		if _, e := tx.Exec(ctx, `UPDATE jobs SET state='running',error='',summary='',started_at=coalesce(started_at,now()) WHERE id=$1`, l.job.ID); e != nil {
			return e
		}
		if l.job.TaskID > 0 {
			if _, e := tx.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id,status) VALUES($1,$2,$3,'running') ON CONFLICT(task_id,agent_name) DO UPDATE SET status='running'`, l.job.TaskID, l.job.Agent, l.job.SessionID); e != nil {
				return e
			}
		}
		_, e := tx.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name) VALUES($1,'agent_started',$2)`, l.eventTask(), l.job.Agent)
		return e
	})
}
func (l *lease) Capture(ctx context.Context, out app.Output) error {
	return l.transaction(ctx, func(tx pgx.Tx) error {
		if summary := capturedSummary(out.Text); summary != "" {
			if _, err := tx.Exec(ctx, `UPDATE jobs SET summary=$2 WHERE id=$1`, l.job.ID, summary); err != nil {
				return err
			}
		}
		if out.SessionID != "" {
			if _, e := tx.Exec(ctx, `UPDATE jobs SET session_id=$2 WHERE id=$1`, l.job.ID, out.SessionID); e != nil {
				return e
			}
			if l.job.TaskID > 0 {
				if _, e := tx.Exec(ctx, `UPDATE sessions SET session_id=$3 WHERE task_id=$1 AND agent_name=$2`, l.job.TaskID, l.job.Agent, out.SessionID); e != nil {
					return e
				}
			}
		}
		_, e := tx.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name,body,job_id) VALUES($1,'agent_output',$2,$3,$4)`, l.eventTask(), l.job.Agent, out.Text, l.job.ID)
		return e
	})
}
func (l *lease) Archive(ctx context.Context, r app.Result) error {
	e := l.transaction(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `UPDATE jobs SET state='archiving',session_id=$2,history=$3 WHERE id=$1`, l.job.ID, r.SessionID, r.History); e != nil {
			return e
		}
		if l.job.TaskID > 0 {
			_, e := tx.Exec(ctx, `UPDATE sessions SET session_id=$3 WHERE task_id=$1 AND agent_name=$2`, l.job.TaskID, l.job.Agent, r.SessionID)
			return e
		}
		return nil
	})
	if e == nil {
		l.job.State = "archiving"
		l.job.SessionID = r.SessionID
	}
	return e
}
func (l *lease) Complete(ctx context.Context) error {
	return l.transaction(ctx, func(tx pgx.Tx) error {
		closed := false
		if l.job.TaskID > 0 {
			var status task.Status
			if e := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 FOR UPDATE`, l.job.TaskID).Scan(&status); e != nil {
				return e
			}
			closed = status == task.Closed
		}
		if err := l.recordSessionRun(ctx, tx, "done", ""); err != nil {
			return err
		}
		if _, e := tx.Exec(ctx, `UPDATE jobs SET state='done',history=NULL,error='' WHERE id=$1`, l.job.ID); e != nil {
			return e
		}
		if l.job.TaskID > 0 {
			if _, e := tx.Exec(ctx, `UPDATE sessions SET status='idle' WHERE task_id=$1 AND agent_name=$2`, l.job.TaskID, l.job.Agent); e != nil {
				return e
			}
		}
		kind := "agent_stopped"
		if l.job.Notification {
			kind = "notification_handled"
		} else if closed {
			kind = "agent_archived"
		}
		_, e := tx.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name,body) VALUES($1,$2,$3,$4)`, l.eventTask(), kind, l.job.Agent, l.job.SessionID)
		return e
	})
}
func (l *lease) Fail(ctx context.Context, cause error) error {
	return l.transaction(ctx, func(tx pgx.Tx) error {
		state := "failed"
		status, kind := "failed", "agent_failed"
		if errors.Is(cause, context.Canceled) || errors.Is(cause, app.ErrTaskClosed) {
			status, kind = "idle", "agent_interrupted"
		}
		if l.job.State == "archiving" && !errors.Is(cause, app.ErrTaskClosed) {
			state = "archiving"
		} else if errors.Is(cause, context.Canceled) {
			state = "pending"
		}
		if state == "failed" {
			outcome := "failed"
			if kind == "agent_interrupted" {
				outcome = "interrupted"
			}
			if err := l.recordSessionRun(ctx, tx, outcome, cause.Error()); err != nil {
				return err
			}
		}
		if _, e := tx.Exec(ctx, `UPDATE jobs SET state=$2,error=$3,available_at=now()+interval '10 seconds' WHERE id=$1`, l.job.ID, state, cause.Error()); e != nil {
			return e
		}
		if l.job.TaskID > 0 {
			if _, e := tx.Exec(ctx, `UPDATE sessions SET status=$3 WHERE task_id=$1 AND agent_name=$2`, l.job.TaskID, l.job.Agent, status); e != nil {
				return e
			}
		}
		_, e := tx.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name,body) VALUES($1,$2,$3,$4)`, l.eventTask(), kind, l.job.Agent, cause.Error())
		return e
	})
}
