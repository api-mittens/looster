package agent

import "time"

// SessionRun describes one queued job. Completed results are durable and
// immutable, independent of the mutable session binding. Detail lookups can also
// return live jobs with outcome pending/running/archiving, summary_status pending
// until captured, and zero FinishedAt (and zero StartedAt before execution).
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
