package app

import (
	"context"
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
)

type logPermissionRepo struct {
	Repository
	input agent.SessionLogInput
	calls int
}

func (r *logPermissionRepo) AppendSessionLog(_ context.Context, in agent.SessionLogInput) (agent.SessionLog, error) {
	r.calls++
	r.input = in
	return agent.SessionLog{JobID: in.JobID, Agent: in.Agent}, nil
}
func (r *logPermissionRepo) TaskSessionLogs(context.Context, int64) ([]agent.SessionLog, error) {
	return []agent.SessionLog{{ID: 1}}, nil
}
func (r *logPermissionRepo) RunSessionLogs(context.Context, int64) ([]agent.SessionLog, error) {
	return []agent.SessionLog{{ID: 2}}, nil
}

func TestSessionLogPermissions(t *testing.T) {
	ctx := context.Background()
	for _, role := range []Role{Human, Agent} {
		raw := &logPermissionRepo{}
		wrapped, err := WithPermissions(raw, Actor{Role: role, Name: "coder"})
		if err != nil {
			t.Fatal(err)
		}
		logs := wrapped.(SessionLogRepository)
		in := agent.SessionLogInput{JobID: 7, Agent: "coder", Kind: "question", Body: "Need clarification"}
		if _, err := logs.AppendSessionLog(ctx, in); err != nil || raw.calls != 1 || raw.input != in {
			t.Fatalf("own write: %+v %v", raw, err)
		}
		in.Agent = "other"
		_, err = logs.AppendSessionLog(ctx, in)
		if role == Agent && !errors.Is(err, ErrForbidden) {
			t.Fatalf("foreign write: %v", err)
		}
		if role == Human && err != nil {
			t.Fatal(err)
		}
		if rows, err := logs.TaskSessionLogs(ctx, 4); err != nil || len(rows) != 1 || rows[0].ID != 1 {
			t.Fatalf("task read: %+v %v", rows, err)
		}
		if rows, err := logs.RunSessionLogs(ctx, 7); err != nil || len(rows) != 1 || rows[0].ID != 2 {
			t.Fatalf("run read: %+v %v", rows, err)
		}
	}
}
func TestSessionLogValidation(t *testing.T) {
	valid := agent.SessionLogInput{JobID: 1, Agent: "coder", Kind: "custom-kind", Body: "body"}
	if err := ValidateSessionLog(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*agent.SessionLogInput){func(i *agent.SessionLogInput) { i.JobID = 0 }, func(i *agent.SessionLogInput) { i.Agent = "bad name" }, func(i *agent.SessionLogInput) { i.Kind = " " }, func(i *agent.SessionLogInput) { i.Body = "\n" }, func(i *agent.SessionLogInput) { i.TaskID = -1 }} {
		in := valid
		change(&in)
		if err := ValidateSessionLog(in); !errors.Is(err, ErrInvalidSessionLog) {
			t.Fatalf("accepted %+v: %v", in, err)
		}
	}
	wrapped, _ := WithPermissions(struct{ Repository }{}, Actor{Role: Human})
	if _, err := wrapped.(SessionLogRepository).AppendSessionLog(context.Background(), valid); !errors.Is(err, ErrSessionHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := wrapped.(SessionLogRepository).TaskSessionLogs(context.Background(), 1); !errors.Is(err, ErrSessionHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := wrapped.(SessionLogRepository).RunSessionLogs(context.Background(), 1); !errors.Is(err, ErrSessionHistoryUnavailable) {
		t.Fatal(err)
	}
}
