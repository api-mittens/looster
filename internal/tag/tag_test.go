package tag

import "testing"

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{{"design/frontend", true}, {"TAG", true}, {"front-end", true}, {"bad2", false}, {"/bad", false}, {"bad/", false}, {"a//b", false}, {"a_b", false}, {"", false}} {
		if ValidName(tc.name) != tc.valid {
			t.Errorf("name %q", tc.name)
		}
	}
}
