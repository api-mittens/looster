package postgres

import (
	"context"
	"errors"

	"github.com/dominikbb/looster/internal/event"
)

func (s *Store) Logs(ctx context.Context, f event.Filter) ([]event.Event, error) {
	if f.Limit < 1 || f.Limit > 10000 || f.After < 0 || f.Until < 0 || f.TaskID < 0 {
		return nil, errors.New("invalid log page bounds")
	}
	if f.Kinds == nil {
		f.Kinds = []string{}
	}
	order := "ASC"
	if f.Desc {
		order = "DESC"
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,task_id,kind,agent_name,body,created_at,actor_agent FROM events
 WHERE id>$1 AND ($2::bigint=0 OR id<=$2) AND ($3::bigint=0 OR task_id=$3)
 AND ($4::text='' OR agent_name=$4 OR actor_agent=$4)
 AND (cardinality($5::text[])=0 OR kind=ANY($5))
 AND ($6::timestamptz IS NULL OR created_at>=$6)
 ORDER BY id `+order+` LIMIT $7`, f.After, f.Until, f.TaskID, f.Agent, f.Kinds, f.Since, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []event.Event{}
	for rows.Next() {
		var e event.Event
		if err = rows.Scan(&e.ID, &e.TaskID, &e.Kind, &e.Agent, &e.Body, &e.CreatedAt, &e.ActorAgent); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// System events have no task and therefore cannot trigger task workflow rules.
func (s *Store) RecordSystemEvent(ctx context.Context, kind, body string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,body) VALUES($1,$2)`, kind, body)
	return err
}
