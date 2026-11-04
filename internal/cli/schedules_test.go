package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/api-mittens/looster/internal/app"
	"strings"
	"testing"
)

type scheduleRecorder struct {
	app.Repository
	calls  int
	id     int64
	status string
	err    error
}

func (r *scheduleRecorder) RunSchedule(_ context.Context, id int64) (app.ScheduleRunResult, error) {
	r.calls++
	r.id = id
	return app.ScheduleRunResult{RuleID: id, Status: r.status, AgentName: "coder"}, r.err
}
func TestBehaviourRun(t *testing.T) {
	for _, status := range []string{"queued", "already_active"} {
		r := &scheduleRecorder{status: status}
		cmd := (&application{repo: r}).behaviourCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"run", "42"})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		var got app.ScheduleRunResult
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if r.calls != 1 || r.id != 42 || got.Status != status || got.AgentName != "coder" || got.RuleID != 42 {
			t.Fatalf("bad run: %+v %+v", r, got)
		}
	}
	for _, args := range [][]string{{"run"}, {"run", "0"}, {"run", "-1"}, {"run", "x"}, {"run", "9223372036854775808"}, {"run", "1", "2"}} {
		r := &scheduleRecorder{}
		cmd := (&application{repo: r}).behaviourCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || r.calls != 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	cmd := (&application{}).behaviourCommand()
	cmd.SetArgs([]string{"run", "1"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "manual schedules") {
		t.Fatalf("unsupported: %v", err)
	}
	sentinel := errors.New("dispatch failed")
	r := &scheduleRecorder{err: sentinel}
	cmd = (&application{repo: r}).behaviourCommand()
	cmd.SetArgs([]string{"run", "1"})
	if err := cmd.Execute(); !errors.Is(err, sentinel) {
		t.Fatalf("error lost: %v", err)
	}
}
func TestAgentManualScheduleDeniedBeforeDatabase(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOOSTER_DATABASE_URL", "invalid")
	t.Setenv("LOOSTER_ROLE", "agent")
	t.Setenv("LOOSTER_AGENT", "coder")
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"behaviour", "run", "1"}, strings.NewReader(""), &out, &out); !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("expected forbidden: %v", err)
	}
}
