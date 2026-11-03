package namespace

import (
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	for _, name := range []string{Default, "team-a", "team_b", "a1", strings.Repeat("a", 52)} {
		if err := Validate(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", "Public", "1team", "../team", "a.b", "a b", "a;DROP SCHEMA public", "équipe", strings.Repeat("a", 53)} {
		if Validate(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
}
