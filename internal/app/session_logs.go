package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

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
	if in.Kind == "link" {
		u, err := url.Parse(in.Body)
		if err != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || strings.ContainsAny(in.Body, "\\") || strings.ContainsFunc(in.Body, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf("%w: link body must be a single absolute HTTP(S) URL with a host and no whitespace", ErrInvalidSessionLog)
		}
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
