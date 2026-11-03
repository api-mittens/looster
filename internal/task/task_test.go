package task

import "testing"

func TestIdentifiersAndStatuses(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want int64
	}{{"TSK-1", 1}, {"TSK-9223372036854775807", 9223372036854775807}, {"TAG-1", 0}, {"TSK-0", 0}, {"TSK--1", 0}, {"TSK-+1", 0}, {"TSK-9223372036854775808", 0}, {"1", 0}} {
		t.Run(tc.s, func(t *testing.T) {
			n, e := ParseID(tc.s)
			if tc.want == 0 {
				if e == nil {
					t.Fatal("accepted invalid ID")
				}
			} else if e != nil || n != tc.want {
				t.Fatalf("%d %v", n, e)
			}
		})
	}
	for _, s := range []Status{Planning, Todo, Validated, Closed} {
		if e := s.Validate(); e != nil {
			t.Fatal(e)
		}
	}
	if err := Status("unvalidated").Validate(); err == nil {
		t.Fatal("accepted the removed unvalidated status")
	}
	if Status("running").Validate() == nil {
		t.Fatal("accepted a session state as task status")
	}
}
