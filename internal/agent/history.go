package agent

import "time"

// SessionRun is a durable, immutable result of one queued job. It is independent
// of the mutable session binding, and survives task or agent removal.
type SessionRun struct {
	JobID         int64     `json:"job_id"`
	TaskID        int64     `json:"task_id"`
	ContextTaskID int64     `json:"context_task_id,omitempty"`
	Agent         string    `json:"agent"`
	SessionID     string    `json:"session_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Outcome       string    `json:"outcome"`
	Summary       string    `json:"summary"`
	SummaryStatus string    `json:"summary_status"`
	Error         string    `json:"error,omitempty"`
}
