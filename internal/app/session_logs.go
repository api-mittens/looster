package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/api-mittens/looster/internal/agent"
)

var ErrInvalidSessionLog = errors.New("invalid session log")

// SessionLogRepository is optional for repositories without durable run storage.
type SessionLogRepository interface {
	AppendSessionLog(context.Context, agent.SessionLogInput) (agent.SessionLog, error)
	TaskSessionLogs(context.Context, int64) ([]agent.SessionLog, error)
	RunSessionLogs(context.Context, int64) ([]agent.SessionLog, error)
}

func ValidateSessionLog(in agent.SessionLogInput) error {
	if in.JobID < 1 || !agent.ValidName(in.Agent) || strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.Body) == "" || in.TaskID < 0 || len(in.Kind) > 100 || len(in.Body) > 1048576 {
		return fmt.Errorf("%w: positive job ID, agent, kind (up to 100 bytes) and body (up to 1 MiB) are required", ErrInvalidSessionLog)
	}
	return nil
}

func (r *accessRepository) AppendSessionLog(ctx context.Context, in agent.SessionLogInput) (agent.SessionLog, error) {
	if r.actor.Role == Agent && in.Agent != r.actor.Name {
		return agent.SessionLog{}, fmt.Errorf("%w: agents may log only their own runs", ErrForbidden)
	}
	if err := ValidateSessionLog(in); err != nil {
		return agent.SessionLog{}, err
	}
	repo, ok := r.Repository.(SessionLogRepository)
	if !ok {
		return agent.SessionLog{}, ErrSessionHistoryUnavailable
	}
	return repo.AppendSessionLog(ctx, in)
}
func (r *accessRepository) TaskSessionLogs(ctx context.Context, id int64) ([]agent.SessionLog, error) {
	repo, ok := r.Repository.(SessionLogRepository)
	if !ok {
		return nil, ErrSessionHistoryUnavailable
	}
	return repo.TaskSessionLogs(ctx, id)
}
func (r *accessRepository) RunSessionLogs(ctx context.Context, id int64) ([]agent.SessionLog, error) {
	repo, ok := r.Repository.(SessionLogRepository)
	if !ok {
		return nil, ErrSessionHistoryUnavailable
	}
	return repo.RunSessionLogs(ctx, id)
}
