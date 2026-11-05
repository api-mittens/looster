package postgres

import (
	"context"
	"errors"

	"github.com/api-mittens/looster/internal/event"
)

// SessionRunOutput pages only output captured with a proven job identity. Old
// events with no job_id are deliberately excluded, never inferred from a task,
// agent or reusable harness session. Callers authorize the run via SessionRun.
func (s *Store) SessionRunOutput(ctx context.Context, jobID, after int64, limit int) ([]event.Event, error) {
	if jobID < 1 || after < 0 {
		return nil, errors.New("invalid run output bounds")
	}
	if limit <= 0 {
		limit = 100
	}
	// One extra row supports the API's has_more lookahead for 1000-row pages.
	if limit > 1001 {
		limit = 1001
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,task_id,kind,agent_name,body,created_at,actor_agent FROM events WHERE job_id=$1 AND kind='agent_output' AND id>$2 ORDER BY id LIMIT $3`, jobID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []event.Event{}
	for rows.Next() {
		var e event.Event
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Kind, &e.Agent, &e.Body, &e.CreatedAt, &e.ActorAgent); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
