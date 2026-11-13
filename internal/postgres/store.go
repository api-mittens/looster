// Package postgres implements transactional persistence and worker coordination.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/memory"
	"github.com/api-mittens/looster/internal/namespace"
	"github.com/api-mittens/looster/internal/postgres/db"
	"github.com/api-mittens/looster/internal/tag"
	"github.com/api-mittens/looster/internal/task"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	Pool     *pgxpool.Pool
	q        *db.Queries
	payloads PayloadStore
}

var _ app.Repository = (*Store)(nil)

func Open(ctx context.Context, url string) (*Store, error) {
	return OpenNamespace(ctx, url, namespace.Default)
}
func (s *Store) Close() { s.Pool.Close() }
func Migrate(ctx context.Context, url string) error {
	return MigrateNamespace(ctx, url, namespace.Default)
}
func affected(tag pgconn.CommandTag, e error) error {
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return errors.New("not found or permission denied")
	}
	return nil
}
func (s *Store) Tasks(ctx context.Context) ([]task.Task, error) {
	return s.tasks(ctx, false)
}

// TasksMetadata returns tasks without session log bodies.
func (s *Store) TasksMetadata(ctx context.Context) ([]task.Task, error) {
	return s.tasks(ctx, true)
}

func (s *Store) tasks(ctx context.Context, metadataOnly bool) ([]task.Task, error) {
	rows, e := s.q.ListTasks(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]task.Task, 0, len(rows))
	for _, r := range rows {
		out = append(out, task.Task{ID: r.ID, Name: r.Name, Description: r.Description, Status: task.Status(r.Status), Tags: r.Tags, CreatedAt: r.CreatedAt.Time, LastActivityAt: r.LastActivityAt.Time, StatusChanges: r.StatusChanges, CommentCount: r.CommentCount})
	}
	if metadataOnly {
		return out, nil
	}
	ids := make([]int64, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	logs, e := s.sessionLogsForIDs(ctx, "task_id", ids)
	if e != nil {
		return nil, e
	}
	for i := range out {
		out[i].SessionLogs = logs[out[i].ID]
	}
	return out, nil
}
func (s *Store) Task(ctx context.Context, id int64) (task.Task, error) {
	t, err := s.TaskMetadata(ctx, id)
	if err != nil {
		return t, err
	}
	t.SessionLogs, err = s.TaskSessionLogs(ctx, id)
	return t, err
}

// TaskMetadata returns a task without session log bodies.
func (s *Store) TaskMetadata(ctx context.Context, id int64) (task.Task, error) {
	r, e := s.q.GetTask(ctx, id)
	if e != nil {
		return task.Task{}, repositoryError(e)
	}
	t := task.Task{ID: r.ID, Name: r.Name, Description: r.Description, Status: task.Status(r.Status), CreatedAt: r.CreatedAt.Time, LastActivityAt: r.LastActivityAt.Time}
	rows, e := s.Pool.Query(ctx, `SELECT tags.name FROM tags JOIN task_tags ON tags.id=task_tags.tag_id WHERE task_id=$1 ORDER BY tags.name`, id)
	if e != nil {
		return t, e
	}
	defer rows.Close()
	t.Tags = []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return t, e
		}
		t.Tags = append(t.Tags, name)
	}
	if e = rows.Err(); e != nil {
		return t, e
	}
	rows.Close()
	return t, nil
}
func (s *Store) CreateTask(ctx context.Context, name, desc string, status task.Status, tags []string) (task.Task, error) {
	if strings.TrimSpace(name) == "" {
		return task.Task{}, errors.New("task name is required")
	}
	if e := status.Validate(); e != nil {
		return task.Task{}, e
	}
	tx, e := s.begin(ctx)
	if e != nil {
		return task.Task{}, e
	}
	defer tx.Rollback(ctx)
	// Conditional entry rules must see the complete initial tag set. Defer the
	// status event until all task tags exist in this same transaction.
	if _, e = tx.Exec(ctx, `SELECT set_config('looster.defer_task_status','true',true)`); e != nil {
		return task.Task{}, e
	}
	id, e := s.q.WithTx(tx).CreateTask(ctx, db.CreateTaskParams{Name: name, Description: desc, Status: string(status)})
	if e != nil {
		return task.Task{}, e
	}
	for _, name := range tags {
		var tid int64
		e = tx.QueryRow(ctx, `SELECT id FROM tags WHERE name=$1`, name).Scan(&tid)
		if e != nil {
			return task.Task{}, fmt.Errorf("tag %q: %w", name, e)
		}
		_, e = tx.Exec(ctx, `INSERT INTO task_tags VALUES($1,$2) ON CONFLICT DO NOTHING`, id, tid)
		if e != nil {
			return task.Task{}, e
		}
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('looster.defer_task_status','false',true)`); e != nil {
		return task.Task{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO events(task_id,kind,body) VALUES($1,'status_changed',$2)`, id, string(status)); e != nil {
		return task.Task{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return task.Task{}, e
	}
	return s.Task(ctx, id)
}
func (s *Store) RemoveTask(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		n, e := s.q.WithTx(tx).RemoveTask(ctx, id)
		if e != nil {
			return e
		}
		if n == 0 {
			return errors.New("task not found")
		}
		return nil
	})
}
func (s *Store) SetStatus(ctx context.Context, id int64, status task.Status) error {
	if e := status.Validate(); e != nil {
		return e
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		n, e := s.q.WithTx(tx).SetTaskStatus(ctx, db.SetTaskStatusParams{ID: id, Status: string(status)})
		if e != nil {
			return e
		}
		if n == 0 {
			return errors.New("task not found")
		}
		return nil
	})
}
func (s *Store) Comment(ctx context.Context, id int64, body string) (int64, error) {
	if strings.TrimSpace(body) == "" {
		return 0, errors.New("comment is empty")
	}
	// Event triggers dispatch human mentions and session notifications atomically
	// with the comment, including comments inserted by other persistence clients.
	var eid int64
	e := s.withTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO events(task_id,kind,body) SELECT id,'comment',$2 FROM tasks WHERE id=$1 RETURNING id`, id, body).Scan(&eid)
	})
	return eid, repositoryError(e)
}
func (s *Store) Events(ctx context.Context, taskID, after int64) ([]event.Event, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,task_id,kind,agent_name,body,created_at,actor_agent FROM events WHERE id>$1 AND ($2::bigint=0 OR task_id=$2) ORDER BY id LIMIT 500`, after, taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []event.Event{}
	for rows.Next() {
		var r event.Event
		if e = rows.Scan(&r.ID, &r.TaskID, &r.Kind, &r.Agent, &r.Body, &r.CreatedAt, &r.ActorAgent); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return s.hydrateEvents(ctx, out)
}
func (s *Store) Tags(ctx context.Context) ([]tag.Tag, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,name,description,NOT manifest_active FROM tags ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []tag.Tag{}
	for rows.Next() {
		var r tag.Tag
		if e = rows.Scan(&r.ID, &r.Name, &r.Description, &r.Inactive); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Tag(ctx context.Context, name string) (tag.Tag, error) {
	var r tag.Tag
	e := s.Pool.QueryRow(ctx, `SELECT id,name,description,NOT manifest_active FROM tags WHERE name=$1`, name).Scan(&r.ID, &r.Name, &r.Description, &r.Inactive)
	return r, repositoryError(e)
}
func (s *Store) CreateTag(ctx context.Context, name, desc string) error {
	return errors.New("tags are defined only by TAG declarations in a behaviour manifest")
}
func (s *Store) RemoveTag(ctx context.Context, name string) error {
	return errors.New("remove TAG declarations by applying a complete behaviour manifest; legacy task tags are retained")
}
func (s *Store) Agents(ctx context.Context) ([]agent.Agent, error) {
	// Use durable jobs and manifest roles rather than active sessions, so a
	// standalone-only agent does not gain an empty task column when a run ends.
	// Deleted-task notifications retain context_task_id and are task evidence.
	rows, e := s.Pool.Query(ctx, `SELECT a.name,a.description,
 (EXISTS(SELECT FROM jobs j WHERE j.agent_name=a.name AND j.task_id IS NULL AND j.context_task_id IS NULL)
  OR EXISTS(SELECT FROM rules r WHERE r.target=a.name AND r.trigger='every'))
 AND NOT EXISTS(SELECT FROM sessions s WHERE s.agent_name=a.name)
 AND NOT EXISTS(SELECT FROM jobs j WHERE j.agent_name=a.name AND (j.task_id IS NOT NULL OR j.context_task_id IS NOT NULL))
 AND NOT EXISTS(SELECT FROM rules r WHERE r.trigger<>'every' AND (r.target=a.name OR r.source=a.name))
 FROM agents a ORDER BY a.name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []agent.Agent{}
	for rows.Next() {
		var r agent.Agent
		if e = rows.Scan(&r.Name, &r.Description, &r.TasklessOnly); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Agent(ctx context.Context, name string) (agent.Agent, error) {
	var r agent.Agent
	e := s.Pool.QueryRow(ctx, `SELECT name,description FROM agents WHERE name=$1`, name).Scan(&r.Name, &r.Description)
	return r, repositoryError(e)
}
func (s *Store) CreateAgent(ctx context.Context, name, desc string) error {
	if !agent.ValidName(name) {
		return errors.New("agent names must start with a letter and contain letters, digits, dashes, or underscores")
	}
	_, e := s.Pool.Exec(ctx, `INSERT INTO agents(name,description) VALUES($1,$2)`, name, desc)
	return e
}
func (s *Store) RemoveAgent(ctx context.Context, name string) error {
	return affected(s.Pool.Exec(ctx, `DELETE FROM agents WHERE name=$1`, name))
}
func memoryOwner(kind string) (string, error) {
	switch kind {
	case "tag":
		return "tag_id=(SELECT id FROM tags WHERE name=$1)", nil
	case "agent":
		return "agent_name=$1", nil
	}
	return "", errors.New("invalid memory owner kind")
}
func (s *Store) Memories(ctx context.Context, kind, name string) ([]memory.Memory, error) {
	where, e := memoryOwner(kind)
	if e != nil {
		return nil, e
	}
	rows, e := s.Pool.Query(ctx, `SELECT id,body,created_at FROM memories WHERE `+where+` ORDER BY id`, name)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []memory.Memory{}
	for rows.Next() {
		var r memory.Memory
		if e = rows.Scan(&r.ID, &r.Body, &r.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) AddMemory(ctx context.Context, kind, name, body string, wipe bool) error {
	where, e := memoryOwner(kind)
	if e != nil {
		return e
	}
	if !wipe && strings.TrimSpace(body) == "" {
		return errors.New("memory is empty")
	}
	tx, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Serialize memory mutations without needing UPDATE privileges on the owner table.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':'||$1,0))`, "looster:memory:"+kind+":"+name); e != nil {
		return e
	}
	var owner string
	table := "agents"
	if kind == "tag" {
		table = "tags"
	}
	if e = tx.QueryRow(ctx, `SELECT name FROM `+table+` WHERE name=$1`, name).Scan(&owner); e != nil {
		return e
	}
	if wipe {
		if _, e = tx.Exec(ctx, `DELETE FROM memories WHERE `+where, name); e != nil {
			return e
		}
	}
	if strings.TrimSpace(body) != "" {
		if kind == "tag" {
			_, e = tx.Exec(ctx, `INSERT INTO memories(tag_id,body) SELECT id,$2 FROM tags WHERE name=$1`, name, body)
		} else {
			_, e = tx.Exec(ctx, `INSERT INTO memories(agent_name,body) VALUES($1,$2)`, name, body)
		}
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) RemoveMemory(ctx context.Context, kind, name string, id int64) error {
	where, e := memoryOwner(kind)
	if e != nil {
		return e
	}
	return affected(s.Pool.Exec(ctx, `DELETE FROM memories WHERE `+where+` AND id=$2`, name, id))
}
func (s *Store) Sessions(ctx context.Context, name string) ([]agent.Session, error) {
	return s.sessions(ctx, name, false)
}

// SessionsMetadata returns sessions without summaries or errors.
func (s *Store) SessionsMetadata(ctx context.Context, name string) ([]agent.Session, error) {
	return s.sessions(ctx, name, true)
}

// BoardSessions returns placement metadata without hydrating run summaries or errors.
func (s *Store) BoardSessions(ctx context.Context) ([]agent.Session, error) {
	return s.sessions(ctx, "", true)
}

func (s *Store) sessions(ctx context.Context, name string, metadataOnly bool) ([]agent.Session, error) {
	return s.sessionsRead(ctx, name, metadataOnly, 0, true)
}

func (s *Store) sessionsRead(ctx context.Context, name string, metadataOnly bool, taskID int64, hydrate bool) ([]agent.Session, error) {
	// Stored task sessions survive process crashes. Taskless runs instead use
	// their durable job identity and disappear when processing finishes. Match
	// Claim's lock identity, including notification jobs with a context task.
	// Resolve previews in this same statement, never by per-session history calls.
	// An executing job wins over newer queued jobs. Otherwise choose the latest
	// queued run or result matching the binding, including history whose job was
	// removed. Join summary/error by that job ID only, not by shared session ID.
	preview := `coalesce(h.summary,j.summary,''),
 coalesce(h.summary_status,CASE WHEN j.summary<>'' THEN 'available'
 WHEN j.state IN ('pending','running','archiving') THEN 'pending'
 WHEN j.state='failed' THEN 'failed' ELSE 'missing' END),
 coalesce(h.error,j.error,'')`
	joins := ` LEFT JOIN jobs j ON j.id=s.job_id LEFT JOIN agent_session_runs h ON h.job_id=s.job_id`
	if metadataOnly {
		preview = `''::text,''::text,''::text`
		joins = ""
	}
	query := `WITH candidates AS (
 SELECT s.task_id,coalesce(r.job_id,0) AS job_id,s.agent_name,s.session_id,s.status,
 'looster:session:'||s.task_id||':'||s.agent_name AS lock_key FROM sessions s
 LEFT JOIN LATERAL (
 SELECT job_id FROM (
 SELECT j.id AS job_id,j.state IN ('running','archiving') AS active FROM jobs j
 WHERE j.task_id=s.task_id AND j.agent_name=s.agent_name
 AND (j.state IN ('pending','running','archiving') OR j.session_id=s.session_id)
 UNION ALL
 SELECT h.job_id,false FROM agent_session_runs h
 WHERE h.task_id=s.task_id AND h.agent_name=s.agent_name AND h.session_id=s.session_id
 ) runs ORDER BY active DESC,job_id DESC LIMIT 1
 ) r ON true
 UNION ALL
 SELECT 0,j.id,j.agent_name,j.session_id,'running',
 CASE WHEN j.context_task_id IS NOT NULL AND j.context_task_id>0
 THEN 'looster:session:'||j.context_task_id||':'||j.agent_name
 ELSE 'looster:job:'||j.id END
 FROM jobs j WHERE j.task_id IS NULL AND j.state IN ('running','archiving')
 ) SELECT s.task_id,s.job_id,s.agent_name,s.session_id,
 CASE WHEN s.status <> 'running' THEN s.status
 WHEN NOT EXISTS (SELECT FROM workers w WHERE w.last_seen>now()-interval '30 seconds' AND s.agent_name=ANY(w.agents)) THEN 'orphaned'
 WHEN NOT EXISTS (SELECT FROM pg_locks l WHERE l.locktype='advisory' AND l.granted
 AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND l.classid=((hashtextextended((CASE WHEN current_schema()='public' THEN '' ELSE current_schema()||':' END)||s.lock_key,0)>>32)&4294967295)::oid
 AND l.objid=(hashtextextended((CASE WHEN current_schema()='public' THEN '' ELSE current_schema()||':' END)||s.lock_key,0)&4294967295)::oid
 AND l.objsubid=1) THEN 'interrupted'
 ELSE 'running' END,` + preview + ` FROM candidates s` + joins
	var args []any
	if name != "" {
		query += ` WHERE s.agent_name=$1`
		args = append(args, name)
	}
	if taskID > 0 {
		args = append(args, taskID)
		if name != "" {
			query += fmt.Sprintf(` AND s.task_id=$%d`, len(args))
		} else {
			query += ` WHERE s.task_id=$1`
		}
	}
	query += ` ORDER BY s.task_id,s.agent_name,s.job_id`
	if taskID > 0 {
		query += ` LIMIT 200`
	}
	rows, e := s.Pool.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []agent.Session{}
	for rows.Next() {
		var r agent.Session
		if e = rows.Scan(&r.TaskID, &r.JobID, &r.Agent, &r.SessionID, &r.Status, &r.Summary, &r.SummaryStatus, &r.Error); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if !hydrate {
		return out, nil
	}
	values := []*string{}
	for i := range out {
		values = append(values, &out[i].Summary, &out[i].Error)
	}
	if err := s.hydrateStrings(ctx, values); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) Bind(ctx context.Context, id int64, name, sid string) error {
	if strings.TrimSpace(sid) == "" {
		return errors.New("session ID is empty")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var locked bool
		if e := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended((CASE WHEN current_schema()='public' THEN '' ELSE current_schema()||':' END)||$1,0))`, fmt.Sprintf("looster:session:%d:%s", id, name)).Scan(&locked); e != nil {
			return e
		}
		if !locked {
			return errors.New("session is currently running")
		}
		// Owning the lock proves there is no active harness. A stale persisted
		// running flag must not prevent rebinding an interrupted session.
		return affected(tx.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id) VALUES($1,$2,$3) ON CONFLICT(task_id,agent_name) DO UPDATE SET session_id=excluded.session_id,status='idle'`, id, name, sid))
	})
}

func (s *Store) AddAttachment(ctx context.Context, a task.Attachment) (task.Attachment, error) {
	e := s.withTx(ctx, func(tx pgx.Tx) error {
		if a.EventID != nil {
			var ok bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM events WHERE id=$1 AND task_id=$2 AND kind='comment')`, *a.EventID, a.TaskID).Scan(&ok); e != nil {
				return e
			}
			if !ok {
				return errors.New("comment does not belong to task")
			}
		}
		e := tx.QueryRow(ctx, `INSERT INTO attachments(task_id,event_id,name,object_key,size) VALUES($1,$2,$3,$4,$5) RETURNING id`, a.TaskID, a.EventID, a.Name, a.Key, a.Size).Scan(&a.ID)
		if e != nil {
			return e
		}
		return nil
	})
	return a, e
}
func (s *Store) Attachments(ctx context.Context, id int64) ([]task.Attachment, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,task_id,event_id,name,object_key,size FROM attachments WHERE task_id=$1 ORDER BY id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []task.Attachment{}
	for rows.Next() {
		var a task.Attachment
		if e = rows.Scan(&a.ID, &a.TaskID, &a.EventID, &a.Name, &a.Key, &a.Size); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Workers(ctx context.Context) ([]app.Worker, error) {
	// to_jsonb(w) also works before migration 007. The web UI can still show
	// worker coverage while older servers have no published settings column.
	rows, e := s.Pool.Query(ctx, `SELECT w.id,w.agents,w.last_seen,to_jsonb(w)->'settings' FROM workers w WHERE w.last_seen>now()-interval '30 seconds' ORDER BY w.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []app.Worker{}
	for rows.Next() {
		var w app.Worker
		var t time.Time
		if e = rows.Scan(&w.ID, &w.Agents, &t, &w.Settings); e != nil {
			return nil, e
		}
		w.LastSeen = t.Format(time.RFC3339)
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func mustSub() fs.FS {
	f, e := fs.Sub(migrations, "migrations")
	if e != nil {
		panic(e)
	}
	return f
}

// Transaction-local attribution cannot leak between pooled connections.
func (s *Store) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('looster.actor_agent',$1,true)`, event.Actor(ctx)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
