package app

import (
	"context"
	"errors"
	"github.com/api-mittens/looster/internal/event"
	"testing"
)

type manualScheduleRecorder struct {
	Repository
	calls int
	actor string
}

func (r *manualScheduleRecorder) RunSchedule(ctx context.Context, id int64) (ScheduleRunResult, error) {
	r.calls++
	r.actor = event.Actor(ctx)
	return ScheduleRunResult{RuleID: id, Status: "queued", AgentName: "coder"}, nil
}
func TestManualSchedulePermissions(t *testing.T) {
	for _, role := range []Role{Human, Agent} {
		r := &manualScheduleRecorder{}
		wrapped, err := WithPermissions(r, Actor{Role: role, Name: "coder"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := wrapped.(ManualScheduleRepository).RunSchedule(event.WithActor(context.Background(), "prior"), 42)
		if role == Agent {
			if !errors.Is(err, ErrForbidden) || r.calls != 0 {
				t.Fatalf("agent allowed: %v", err)
			}
		} else if err != nil || r.calls != 1 || r.actor != "" || result.RuleID != 42 {
			t.Fatalf("human: %+v %v %+v", result, err, r)
		}
	}
	wrapped, _ := WithPermissions(fakeRepo{}, Actor{Role: Human})
	if _, err := wrapped.(ManualScheduleRepository).RunSchedule(context.Background(), 1); err == nil {
		t.Fatal("unsupported port accepted")
	}
}
