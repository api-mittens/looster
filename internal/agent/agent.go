package agent

import "regexp"

var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func ValidName(s string) bool { return validName.MatchString(s) }

type Agent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// TasklessOnly is durable board placement metadata, not current liveness.
	TasklessOnly bool `json:"taskless_only,omitempty"`
}
type Session struct {
	// JobID identifies the latest/current run, including runs without a session ID.
	JobID         int64  `json:"job_id,omitempty"`
	Summary       string `json:"summary"`
	SummaryStatus string `json:"summary_status"`
	Error         string `json:"error,omitempty"`
	TaskID        int64  `json:"task_id"`
	Agent         string `json:"agent"`
	SessionID     string `json:"session_id"`
	Status        string `json:"status"`
}
