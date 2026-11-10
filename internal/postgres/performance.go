package postgres

import (
	"context"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

var _ app.PerformanceRepository = (*Store)(nil)

// PerformanceData uses two bounded queries on the namespace-scoped pool. Logs
// are read directly, never joined to runs or tasks (including deleted tasks).
func (s *Store) PerformanceData(ctx context.Context, start, end time.Time) ([]task.Task, []agent.SessionLog, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,status,last_activity_at FROM (
 SELECT t.id,t.status,GREATEST(t.created_at,
 (SELECT max(e.created_at) FROM events e WHERE e.task_id=t.id),
 (SELECT max(l.created_at) FROM session_logs l WHERE l.task_id=t.id)) AS last_activity_at
 FROM tasks t WHERE t.status IN ('validated','closed')
 ) current_tasks WHERE last_activity_at >= $1 AND last_activity_at <= $2
 ORDER BY last_activity_at DESC,id DESC`, start, end)
	if err != nil {
		return nil, nil, err
	}
	tasks := []task.Task{}
	for rows.Next() {
		var t task.Task
		if err := rows.Scan(&t.ID, &t.Status, &t.LastActivityAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		tasks = append(tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT `+sessionLogColumns+` FROM session_logs WHERE created_at >= $1 AND created_at <= $2 AND kind IN ('blocker','question') ORDER BY created_at,id`, start, end)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	logs := []agent.SessionLog{}
	for rows.Next() {
		l, err := scanSessionLog(rows)
		if err != nil {
			return nil, nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	values := []*string{}
	for i := range logs {
		values = append(values, &logs[i].Body)
	}
	if err := s.hydrateStrings(ctx, values); err != nil {
		return nil, nil, err
	}
	return tasks, logs, nil
}
