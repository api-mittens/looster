package agent

import "regexp"

var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func ValidName(s string) bool { return validName.MatchString(s) }

type Agent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type Session struct {
	TaskID    int64  `json:"task_id"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}
