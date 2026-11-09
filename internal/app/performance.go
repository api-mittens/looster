package app

import (
	"context"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

// PerformanceRepository optionally supplies current tasks ordered by last activity
// and immutable observations in [start, end]. It must not use limited run history.
// Implementations must use the same namespace scope as their other queries.
type PerformanceRepository interface {
	PerformanceData(context.Context, time.Time, time.Time) ([]task.Task, []agent.SessionLog, error)
}
