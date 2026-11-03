package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dominikbb/looster/agent"
	"github.com/dominikbb/looster/app"
	"github.com/dominikbb/looster/task"
)

type overviewRepo struct{ app.Repository }

func (overviewRepo) Tasks(context.Context) ([]task.Task, error) {
	return []task.Task{{ID: 1, Name: "Build settings", Status: task.Todo}, {ID: 2, Name: "Plan next step", Status: task.Planning}}, nil
}
func (overviewRepo) Sessions(context.Context, string) ([]agent.Session, error) {
	return []agent.Session{
		{TaskID: 1, Agent: "coder", Status: "running", SessionID: "session_coder"},
		{TaskID: 1, Agent: "reviewer", Status: "idle", SessionID: "session_reviewer"},
	}, nil
}

func TestTaskPSFormats(t *testing.T) {
	for _, format := range []string{"", "json", "xml"} {
		t.Run("format="+format, func(t *testing.T) {
			a := &application{repo: overviewRepo{}}
			cmd := a.taskCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			args := []string{"ps", "--once"}
			if format != "" {
				args = append(args, "--format", format)
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if format == "xml" {
				if err == nil || !strings.Contains(err.Error(), "format must be") {
					t.Fatalf("invalid format: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "\033") {
				t.Fatal("snapshot contains terminal controls")
			}
			if format == "json" {
				var snapshot struct {
					Tasks []struct {
						Ref string `json:"ref"`
					}
					Sessions []agent.Session
				}
				if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Tasks) != 2 || snapshot.Tasks[0].Ref != "TSK-1" || len(snapshot.Sessions) != 2 || snapshot.Sessions[0].SessionID != "session_coder" {
					t.Fatalf("JSON snapshot lost data: %s", out.String())
				}
				return
			}
			for _, want := range []string{"| TASK", "STATUS", "NAME", "AGENT", "STATE", "SESSION", "Build settings", "Plan next step", "session_coder", "session_reviewer"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("table missing %q: %s", want, out.String())
				}
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			for _, line := range lines {
				if len(line) != len(lines[0]) {
					t.Errorf("table borders do not align: %s", out.String())
				}
			}
		})
	}
}
