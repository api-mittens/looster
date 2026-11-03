package tag

import "regexp"

var validName = regexp.MustCompile(`^[A-Za-z-]+(/[A-Za-z-]+)*$`)

func ValidName(s string) bool { return validName.MatchString(s) }

type Tag struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Inactive definitions retain task assignments and memories for history,
	// but must not contribute instructions to current worker prompts.
	Inactive bool `json:"inactive,omitempty"`
}
