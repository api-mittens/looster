package agent

import "regexp"

var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func ValidName(s string) bool { return validName.MatchString(s) }

type Agent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type Session struct {
	// JobID identifies a taskless run before the harness supplies a session ID.
	JobID     int64  `json:"job_id,omitempty"`
	TaskID    int64  `json:"task_id"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}
