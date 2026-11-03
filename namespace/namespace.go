// Package namespace defines portable logical database namespace names.
package namespace

import (
	"fmt"
	"regexp"
)

const Default = "default"

var valid = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,51}$`)

// Validate accepts lowercase names that fit PostgreSQL's 63-byte identifier limit.
func Validate(name string) error {
	if !valid.MatchString(name) {
		return fmt.Errorf("invalid namespace %q: use 1-52 lowercase letters, digits, underscores or dashes, starting with a letter", name)
	}
	return nil
}
