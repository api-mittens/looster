package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/jackc/pgx/v5"
)

const sessionLogColumns = `id,job_id,session_id,agent_name,kind,body,created_at,task_id`

func scanSessionLog(row pgx.Row) (agent.SessionLog, error) {
	var l agent.SessionLog
	err := row.Scan(&l.ID, &l.JobID, &l.SessionID, &l.Agent, &l.Kind, &l.Body, &l.CreatedAt, &l.TaskID)
	return l, err
}

func (s *Store) AppendSessionLog(ctx context.Context, in agent.SessionLogInput) (agent.SessionLog, error) {
	if err := app.ValidateSessionLog(in); err != nil {
		return agent.SessionLog{}, err
	}
	// Resolve only by job primary key, never by latest session or agent. A single
	// statement remains consistent across the live-to-history transactional handoff.
	l, err := scanSessionLog(s.Pool.QueryRow(ctx, `WITH identity AS (
 SELECT job_id,agent_name,session_id,coalesce(nullif(task_id,0),context_task_id) AS task_id FROM agent_session_runs WHERE job_id=$1
 UNION ALL
 SELECT id,agent_name,session_id,coalesce(task_id,context_task_id,0) FROM jobs WHERE id=$1
 AND NOT EXISTS(SELECT FROM agent_session_runs WHERE job_id=$1)
 ) INSERT INTO session_logs(job_id,agent_name,session_id,task_id,kind,body)
 SELECT job_id,agent_name,session_id,task_id,$5,$6 FROM identity
 WHERE agent_name=$2 AND btrim(session_id)<>'' AND ($3='' OR session_id=$3) AND ($4::bigint=0 OR task_id=$4)
 RETURNING `+sessionLogColumns, in.JobID, in.Agent, in.SessionID, in.TaskID, strings.TrimSpace(in.Kind), in.Body))
	if err == pgx.ErrNoRows {
		return agent.SessionLog{}, fmt.Errorf("%w: job, agent, session or task does not match a proven session run", app.ErrInvalidSessionLog)
	}
	return l, err
}

func (s *Store) sessionLogs(ctx context.Context, clause string, id int64) ([]agent.SessionLog, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+sessionLogColumns+` FROM session_logs WHERE `+clause+`=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agent.SessionLog{}
	for rows.Next() {
		l, err := scanSessionLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) TaskSessionLogs(ctx context.Context, id int64) ([]agent.SessionLog, error) {
	return s.sessionLogs(ctx, "task_id", id)
}
func (s *Store) RunSessionLogs(ctx context.Context, id int64) ([]agent.SessionLog, error) {
	return s.sessionLogs(ctx, "job_id", id)
}

// Batch loading keeps board and history listing at a constant query count.
func (s *Store) sessionLogsForIDs(ctx context.Context, column string, ids []int64) (map[int64][]agent.SessionLog, error) {
	out := make(map[int64][]agent.SessionLog, len(ids))
	for _, id := range ids {
		out[id] = []agent.SessionLog{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+sessionLogColumns+` FROM session_logs WHERE `+column+`=ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanSessionLog(rows)
		if err != nil {
			return nil, err
		}
		id := l.JobID
		if column == "task_id" {
			id = l.TaskID
		}
		out[id] = append(out[id], l)
	}
	return out, rows.Err()
}
