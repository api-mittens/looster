package postgres

import (
	"context"
	"errors"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

// TaskPayloadDetail reads one task and a bounded run history page.
// It reads payload text in one batch if the payload store supports batch reads.
// A zero limit selects 50 runs.
// The maximum limit is 200 runs. The cursor excludes beforeJobID.
// It includes at most 200 task logs in ascending ID order.
func (s *Store) TaskPayloadDetail(ctx context.Context, taskID, beforeJobID int64, limit int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	if limit > 200 {
		limit = 200
	}
	return s.TaskPayloadDetailPage(ctx, taskID, beforeJobID, 0, limit, 200)
}

// TaskPayloadDetailPage reads bounded task text with independent run and log cursors.
// Each cursor is exclusive. A zero cursor starts at the newest record.
// At most 200 sessions and 8 MiB of payload text are returned.
// Run and log limits permit 201 rows for a 200-row page with one extra row.
func (s *Store) TaskPayloadDetailPage(ctx context.Context, taskID, beforeJobID, beforeLogID int64, limit, logLimit int) (task.Task, []agent.Session, []agent.SessionRun, error) {
	if taskID < 1 || beforeJobID < 0 || beforeLogID < 0 {
		return task.Task{}, nil, nil, errors.New("invalid task detail bounds")
	}
	t, err := s.TaskMetadata(ctx, taskID)
	if err != nil {
		return t, nil, nil, err
	}
	sessions, err := s.sessionsRead(ctx, "", false, taskID, false)
	if err != nil {
		return task.Task{}, nil, nil, err
	}
	runs, err := s.sessionHistoryRead(ctx, "", taskID, beforeJobID, limit, false, false)
	if err != nil {
		return task.Task{}, nil, nil, err
	}
	t.SessionLogs, err = s.taskDetailLogs(ctx, taskID, beforeLogID, logLimit)
	if err != nil {
		return task.Task{}, nil, nil, err
	}
	values := []*string{}
	for i := range t.SessionLogs {
		values = append(values, &t.SessionLogs[i].Body)
	}
	for i := range sessions {
		values = append(values, &sessions[i].Summary, &sessions[i].Error)
	}
	for i := range runs {
		values = append(values, &runs[i].Summary, &runs[i].Error)
	}
	if err := s.hydrateDetailStrings(ctx, values); err != nil {
		return task.Task{}, nil, nil, err
	}
	var bytes int64
	for _, value := range values {
		bytes += int64(len(*value))
	}
	for i := range runs {
		runs[i].Logs = []agent.SessionLog{}
		for _, log := range t.SessionLogs {
			if log.JobID == runs[i].JobID {
				bytes += int64(len(log.Body))
				if bytes > TaskPayloadMaxBytes {
					return task.Task{}, nil, nil, ErrTaskPayloadLimit
				}
				runs[i].Logs = append(runs[i].Logs, log)
			}
		}
	}
	return t, sessions, runs, nil
}

func (s *Store) taskDetailLogs(ctx context.Context, taskID, beforeLogID int64, limit int) ([]agent.SessionLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 201 {
		limit = 201
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+sessionLogColumns+` FROM (SELECT `+sessionLogColumns+` FROM session_logs WHERE task_id=$1 AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT $3) recent ORDER BY id`, taskID, beforeLogID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agent.SessionLog{}
	for rows.Next() {
		log, err := scanSessionLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}
