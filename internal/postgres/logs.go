package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dominikbb/looster/internal/event"
)

// Emit only selected predicates. Optional-parameter OR clauses conceal selective
// task indexes once PostgreSQL switches a prepared statement to a generic plan.
// Values remain parameters and the number of query shapes is bounded.
func logQuery(f event.Filter) (string, []any) {
	var q strings.Builder
	q.WriteString(`SELECT id,task_id,kind,agent_name,body,created_at,actor_agent FROM events WHERE id>$1`)
	args := []any{f.After}
	add := func(predicate string, value any) {
		args = append(args, value)
		q.WriteString(" AND " + fmt.Sprintf(predicate, len(args)))
	}
	if f.Until != 0 {
		add("id<=$%d", f.Until)
	}
	if f.TaskID != 0 {
		add("task_id=$%d", f.TaskID)
	}
	if f.Agent != "" {
		args = append(args, f.Agent)
		n := len(args)
		fmt.Fprintf(&q, " AND (agent_name=$%d OR actor_agent=$%d)", n, n)
	}
	if len(f.Kinds) != 0 {
		add("kind=ANY($%d::text[])", f.Kinds)
	}
	if f.Since != nil {
		add("created_at>=$%d", *f.Since)
	}
	q.WriteString(" ORDER BY id ")
	if f.Desc {
		q.WriteString("DESC")
	} else {
		q.WriteString("ASC")
	}
	args = append(args, f.Limit)
	fmt.Fprintf(&q, " LIMIT $%d", len(args))
	return q.String(), args
}

func (s *Store) Logs(ctx context.Context, f event.Filter) ([]event.Event, error) {
	if f.Limit < 1 || f.Limit > 10000 || f.After < 0 || f.Until < 0 || f.TaskID < 0 {
		return nil, errors.New("invalid log page bounds")
	}
	query, args := logQuery(f)
	rows, err := s.Pool.Query(ctx, query, args...)
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
