package agent

import "time"

// SessionLog is an immutable observation attributed to a proven queued run.
type SessionLog struct {
	ID        int64     `json:"id"`
	JobID     int64     `json:"job_id"`
	SessionID string    `json:"session_id"`
	Agent     string    `json:"agent"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	TaskID    int64     `json:"task_id"`
}

type SessionLogInput struct {
	JobID     int64  `json:"job_id"`
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
	TaskID    int64  `json:"task_id"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
}
