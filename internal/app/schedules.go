package app

import (
	"context"
	"errors"
)

// ManualScheduleRepository dispatches an active recurring rule without changing its cadence.
type ManualScheduleRepository interface {
	RunSchedule(context.Context, int64) (ScheduleRunResult, error)
}

type ScheduleRunResult struct {
	RuleID    int64  `json:"rule_id"`
	Status    string `json:"status"`
	AgentName string `json:"agent_name"`
}

func (r *accessRepository) RunSchedule(ctx context.Context, id int64) (ScheduleRunResult, error) {
	if err := r.actor.RequireHuman(); err != nil {
		return ScheduleRunResult{}, err
	}
	repo, ok := r.Repository.(ManualScheduleRepository)
	if !ok {
		return ScheduleRunResult{}, errors.New("repository does not support manual schedules")
	}
	return repo.RunSchedule(r.context(ctx), id)
}
