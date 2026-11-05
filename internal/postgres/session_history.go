package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/jackc/pgx/v5"
)

const sessionRunColumns = `job_id,task_id,context_task_id,agent_name,session_id,started_at,finished_at,outcome,summary,summary_status,error`

// SessionHistory returns newest jobs first. Zero filters mean all tasks/agents;
// beforeJobID is an exclusive cursor. Limits are bounded independently of callers.
func (s *Store) SessionHistory(ctx context.Context, name string, taskID, beforeJobID int64, limit int) ([]agent.SessionRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+sessionRunColumns+` FROM agent_session_runs WHERE ($1='' OR agent_name=$1) AND ($2::bigint=0 OR task_id=$2 OR context_task_id=$2) AND ($3::bigint=0 OR job_id<$3) ORDER BY job_id DESC LIMIT $4`, name, taskID, beforeJobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agent.SessionRun{}
	for rows.Next() {
		r, err := scanSessionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SessionRun(ctx context.Context, jobID int64) (agent.SessionRun, error) {
	// Prefer the immutable result. Live jobs expose only inspector metadata, never
	// prompts, archive bytes or arbitrary subprocess output. A single statement
	// gives a consistent snapshot across completion's transactional handoff.
	r, err := scanSessionRun(s.Pool.QueryRow(ctx, `SELECT `+sessionRunColumns+` FROM agent_session_runs WHERE job_id=$1
 UNION ALL
 SELECT id,coalesce(task_id,0),coalesce(context_task_id,0),agent_name,session_id,
 coalesce(started_at,'0001-01-01 00:00:00+00'::timestamptz),'0001-01-01 00:00:00+00'::timestamptz,state,summary,
 CASE WHEN summary<>'' THEN 'available' ELSE 'pending' END,error
 FROM jobs WHERE id=$1 AND state IN ('pending','running','archiving')
 AND NOT EXISTS (SELECT FROM agent_session_runs WHERE job_id=$1)`, jobID))
	return r, repositoryError(err)
}

func scanSessionRun(row pgx.Row) (agent.SessionRun, error) {
	var r agent.SessionRun
	err := row.Scan(&r.JobID, &r.TaskID, &r.ContextTaskID, &r.Agent, &r.SessionID, &r.StartedAt, &r.FinishedAt, &r.Outcome, &r.Summary, &r.SummaryStatus, &r.Error)
	return r, err
}

// Only a structured done event is a summary, never arbitrary subprocess output.
func capturedSummary(text string) string {
	var output struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(text), &output) != nil || output.Type != "done" {
		return ""
	}
	return strings.TrimSpace(output.Text)
}

func (l *lease) recordSessionRun(ctx context.Context, tx pgx.Tx, outcome, cause string) error {
	_, err := tx.Exec(ctx, `INSERT INTO agent_session_runs(job_id,task_id,context_task_id,agent_name,session_id,started_at,outcome,summary,summary_status,error)
 SELECT id,coalesce(task_id,0),coalesce(context_task_id,0),agent_name,session_id,coalesce(started_at,now()),$2,summary,
 CASE WHEN summary<>'' THEN 'available' WHEN $2='done' THEN 'missing' ELSE $2 END,$3 FROM jobs WHERE id=$1
 ON CONFLICT(job_id) DO NOTHING`, l.job.ID, outcome, cause)
	return err
}
