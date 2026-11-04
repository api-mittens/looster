package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/jackc/pgx/v5"
)

var _ app.ManualScheduleRepository = (*Store)(nil)

// RunSchedule shares the scheduler's rule lock and durable active-job exclusion.
// Namespace-scoped pools resolve every relation in the selected namespace.
func (s *Store) RunSchedule(ctx context.Context, id int64) (app.ScheduleRunResult, error) {
	result := app.ScheduleRunResult{RuleID: id}
	if event.Actor(ctx) != "" {
		return result, app.ErrForbidden
	}
	if id < 1 {
		return result, errors.New("rule ID must be positive")
	}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var prompt string
		if err := tx.QueryRow(ctx, `SELECT target,prompt FROM rules WHERE id=$1 AND trigger='every' FOR UPDATE`, id).Scan(&result.AgentName, &prompt); err != nil {
			return fmt.Errorf("schedule rule %d: %w", id, repositoryError(err))
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM jobs WHERE schedule_rule_id=$1 AND state IN ('pending','running','archiving'))`, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			result.Status = "already_active"
			return nil
		}
		var eventID int64
		if err := tx.QueryRow(ctx, `INSERT INTO events(kind,agent_name,body) VALUES('scheduled',$1,$2) RETURNING id`, result.AgentName, prompt).Scan(&eventID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO jobs(agent_name,event_id,prompt,schedule_rule_id) VALUES($1,$2,$3,$4)`, result.AgentName, eventID, prompt, id); err != nil {
			return err
		}
		result.Status = "queued"
		return nil
	})
	return result, err
}
