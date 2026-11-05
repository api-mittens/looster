package app

import (
	"context"
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
)

type historyPermissionRepo struct {
	Repository
	name           string
	taskID, before int64
	limit          int
	failure        error
}

func (r *historyPermissionRepo) SessionHistory(_ context.Context, name string, taskID, before int64, limit int) ([]agent.SessionRun, error) {
	r.name, r.taskID, r.before, r.limit = name, taskID, before, limit
	return []agent.SessionRun{{JobID: 12, Agent: name, Summary: "Finished"}}, r.failure
}
func (r *historyPermissionRepo) SessionRun(_ context.Context, id int64) (agent.SessionRun, error) {
	return agent.SessionRun{JobID: id, Agent: "solo"}, r.failure
}

func TestPermissionSessionHistoryForwarding(t *testing.T) {
	for _, role := range []Role{Human, Agent} {
		t.Run(string(role), func(t *testing.T) {
			raw := &historyPermissionRepo{}
			wrapped, err := WithPermissions(raw, Actor{Role: role, Name: "coder"})
			if err != nil {
				t.Fatal(err)
			}
			history, ok := wrapped.(SessionHistoryRepository)
			if !ok {
				t.Fatal("permission wrapper dropped run inspection")
			}
			runs, err := history.SessionHistory(context.Background(), "solo", 42, 20, 7)
			if err != nil || len(runs) != 1 || raw.name != "solo" || raw.taskID != 42 || raw.before != 20 || raw.limit != 7 {
				t.Fatalf("history forwarding: %+v %+v %v", raw, runs, err)
			}
			run, err := history.SessionRun(context.Background(), 12)
			if err != nil || run.JobID != 12 {
				t.Fatalf("detail forwarding: %+v %v", run, err)
			}
			raw.failure = errors.New("storage error")
			if _, err = history.SessionHistory(context.Background(), "solo", 0, 0, 1); !errors.Is(err, raw.failure) {
				t.Fatal(err)
			}
			if _, err = history.SessionRun(context.Background(), 12); !errors.Is(err, raw.failure) {
				t.Fatal(err)
			}
		})
	}
}

func TestPermissionSessionHistoryUnavailable(t *testing.T) {
	wrapped, err := WithPermissions(struct{ Repository }{}, Actor{Role: Human})
	if err != nil {
		t.Fatal(err)
	}
	history := wrapped.(SessionHistoryRepository)
	if _, err := history.SessionHistory(context.Background(), "solo", 0, 0, 20); !errors.Is(err, ErrSessionHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := history.SessionRun(context.Background(), 1); !errors.Is(err, ErrSessionHistoryUnavailable) {
		t.Fatal(err)
	}
}
