package task

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	Planning  Status = "planning"
	Todo      Status = "todo"
	Validated Status = "validated"
	Closed    Status = "closed"
)

func (s Status) Validate() error {
	switch s {
	case Planning, Todo, Validated, Closed:
		return nil
	}
	return fmt.Errorf("invalid status %q: use planning, todo, validated, or closed", s)
}
func ID(n int64) string               { return fmt.Sprintf("TSK-%d", n) }
func ParseID(s string) (int64, error) { return ParsePrettyID(s, "TSK") }
func ParsePrettyID(s, prefix string) (int64, error) {
	if !strings.HasPrefix(s, prefix+"-") {
		return 0, fmt.Errorf("expected %s-n ID, got %q", prefix, s)
	}
	digits := strings.TrimPrefix(s, prefix+"-")
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid %s ID %q", prefix, s)
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid %s ID %q", prefix, s)
	}
	return n, nil
}

type Task struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Status         Status    `json:"status"`
	Tags           []string  `json:"tags"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	StatusChanges  int64     `json:"status_changes"`
	CommentCount   int64     `json:"comment_count"`
}
type Attachment struct {
	ID      int64  `json:"id"`
	TaskID  int64  `json:"task_id"`
	EventID *int64 `json:"event_id,omitempty"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Size    int64  `json:"size"`
}
