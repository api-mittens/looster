package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/api-mittens/looster/internal/web"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/viper"
)

type cliLogRepo struct {
	app.Repository
	input agent.SessionLogInput
	calls int
}

func TestSessionLogLinkCLIAndAPI(t *testing.T) {
	t.Setenv("LOOSTER_JOB_ID", "9")
	t.Setenv("LOOSTER_AGENT", "coder")
	t.Setenv("LOOSTER_SESSION_ID", "session")
	t.Setenv("LOOSTER_TASK_ID", "TSK-3")
	for _, body := range []string{"https://example.com/path?a=1&b=2", "http://localhost:8080", "https:///missing", "javascript:alert(1)", "https://one.test https://two.test", "https://example.com\n", "https://example.com/\u00a0"} {
		t.Run(body, func(t *testing.T) {
			valid := body == "https://example.com/path?a=1&b=2" || body == "http://localhost:8080"
			raw := &cliLogRepo{}
			repo, _ := app.WithPermissions(raw, app.Actor{Role: app.Agent, Name: "coder"})
			cmd := (&application{repo: repo}).sessionLogCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"link", body})
			err := cmd.ExecuteContext(context.Background())
			if valid {
				if err != nil || raw.calls != 1 || raw.input.Body != body || raw.input.Kind != "link" || raw.input.JobID != 9 || raw.input.Agent != "coder" || raw.input.SessionID != "session" || raw.input.TaskID != 3 {
					t.Fatalf("CLI input=%+v calls=%d err=%v", raw.input, raw.calls, err)
				}
			} else if !errors.Is(err, app.ErrInvalidSessionLog) || raw.calls != 0 {
				t.Fatalf("CLI accepted invalid link: %v calls=%d", err, raw.calls)
			}
			raw.calls = 0
			payload, _ := json.Marshal(map[string]string{"kind": "link", "body": body, "session_id": "session"})
			req := httptest.NewRequest("POST", "/api/agents/coder/runs/9/logs", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			web.Handler(repo, nil).ServeHTTP(w, req)
			want, calls := 400, 0
			if valid {
				want, calls = 200, 1
			}
			if w.Code != want || raw.calls != calls {
				t.Fatalf("API status=%d calls=%d body=%s", w.Code, raw.calls, w.Body.String())
			}
			if valid && (raw.input.JobID != 9 || raw.input.Agent != "coder" || raw.input.SessionID != "session" || raw.input.Body != body) {
				t.Fatalf("API attribution: %+v", raw.input)
			}
		})
	}
}

func TestSessionLogCLIHTTPPostgres(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	for _, name := range []string{"ENDPOINT", "BUCKET", "ACCESS_KEY", "SECRET_KEY"} {
		value := os.Getenv("LOOSTER_TEST_S3_" + name)
		if value == "" {
			t.Skip("requires explicit disposable LOOSTER_TEST_S3_" + name)
		}
		t.Setenv("LOOSTER_S3_"+name, value)
	}
	t.Setenv("LOOSTER_S3_SESSION_TOKEN", "")
	t.Setenv("LOOSTER_DUCKLAKE_DATABASE", "")
	t.Setenv("LOOSTER_DUCKLAKE_DATA_PATH", "")
	t.Setenv("LOOSTER_DUCKLAKE_SPOOL_DIR", t.TempDir())
	ns := fmt.Sprintf("log_cli_%d", time.Now().UnixNano())
	if err := postgres.EnsureNamespace(ctx, url, ns); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.OpenNamespace(ctx, url, ns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer store.Close()
		if _, err := store.Pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{"looster_ns_" + ns}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.ApplyManifest(ctx, `AGENT coder WITH "Implement";`); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := store.Pool.QueryRow(ctx, `WITH e AS (INSERT INTO events(kind,agent_name) VALUES('scheduled','coder') RETURNING id) INSERT INTO jobs(agent_name,event_id,session_id) SELECT 'coder',id,'session-proof' FROM e RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOOSTER_DATABASE_URL", url)
	t.Setenv("LOOSTER_NAMESPACE", ns)
	t.Setenv("LOOSTER_ROLE", "agent")
	t.Setenv("LOOSTER_AGENT", "coder")
	t.Setenv("LOOSTER_JOB_ID", fmt.Sprint(jobID))
	t.Setenv("LOOSTER_SESSION_ID", "")
	t.Setenv("LOOSTER_TASK_ID", "")
	var out bytes.Buffer
	if err := Execute(ctx, []string{"task", "log", "blocker", "Need API access"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("CLI: %s %v", out.String(), err)
	}
	var saved agent.SessionLog
	if err := json.Unmarshal(out.Bytes(), &saved); err != nil || saved.SessionID != "session-proof" || saved.TaskID != 0 || saved.JobID != jobID {
		t.Fatalf("saved=%+v %s %v", saved, out.String(), err)
	}
	readerApp := &application{v: viper.New()}
	_ = readerApp.command() // Configure environment resolution without running a command.
	reader, err := readerApp.openPayloadStore(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	store.SetPayloadStore(reader)
	repo, err := app.WithPermissions(store, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	h := web.Handler(repo, nil)
	for _, path := range []string{fmt.Sprintf("/api/agents/coder/runs/%d", jobID), fmt.Sprintf("/api/agents/coder/runs/%d/logs", jobID)} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"body":"Need API access"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	out.Reset()
	if err := Execute(ctx, []string{"task", "log", "question", "Forged", "--session", "invented"}, strings.NewReader(""), &out, &out); !errors.Is(err, app.ErrInvalidSessionLog) {
		t.Fatalf("forged CLI: %v", err)
	}
}

func (r *cliLogRepo) AppendSessionLog(_ context.Context, in agent.SessionLogInput) (agent.SessionLog, error) {
	r.calls++
	r.input = in
	return agent.SessionLog{ID: 1, JobID: in.JobID, Agent: in.Agent, SessionID: "session", Kind: in.Kind, Body: in.Body}, nil
}
func (r *cliLogRepo) TaskSessionLogs(context.Context, int64) ([]agent.SessionLog, error) {
	return []agent.SessionLog{{ID: 1, JobID: 9, Agent: "coder", Kind: "question", Body: "body"}}, nil
}
func (r *cliLogRepo) RunSessionLogs(context.Context, int64) ([]agent.SessionLog, error) {
	return []agent.SessionLog{}, nil
}
func (r *cliLogRepo) Task(context.Context, int64) (task.Task, error) { return task.Task{ID: 3}, nil }
func (r *cliLogRepo) SessionRun(_ context.Context, id int64) (agent.SessionRun, error) {
	if id != 9 {
		return agent.SessionRun{}, app.ErrNotFound
	}
	return agent.SessionRun{JobID: id, Agent: "coder", SessionID: "session"}, nil
}
func (r *cliLogRepo) SessionHistory(context.Context, string, int64, int64, int) ([]agent.SessionRun, error) {
	return nil, nil
}

func TestSessionLogCLIExplicitAndCurrentRun(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Setenv("LOOSTER_JOB_ID", "9")
		t.Setenv("LOOSTER_AGENT", "coder")
		t.Setenv("LOOSTER_SESSION_ID", "session")
		t.Setenv("LOOSTER_TASK_ID", "")
		raw := &cliLogRepo{}
		repo, _ := app.WithPermissions(raw, app.Actor{Role: app.Agent, Name: "coder"})
		cmd := (&application{repo: repo}).sessionLogCommand()
		args := []string{"question", "Which option?"}
		if explicit {
			args = append(args, "--job", "10", "--agent", "coder", "--session", "explicit", "--task", "TSK-3")
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := int64(9)
		if explicit {
			want = 10
		}
		if raw.calls != 1 || raw.input.JobID != want || raw.input.Agent != "coder" || raw.input.Kind != "question" {
			t.Fatalf("input %+v", raw.input)
		}
		var result agent.SessionLog
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.JobID != want {
			t.Fatalf("output %s %v", out.String(), err)
		}
	}
	t.Setenv("LOOSTER_JOB_ID", "")
	cmd := (&application{repo: &cliLogRepo{}}).sessionLogCommand()
	cmd.SetArgs([]string{"blocker", "body"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted unbound log")
	}
}

func TestSessionLogHTTPContractsAndPermissions(t *testing.T) {
	raw := &cliLogRepo{}
	repo, _ := app.WithPermissions(raw, app.Actor{Role: app.Agent, Name: "coder"})
	handler := web.Handler(repo, nil)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/tasks/TSK-3/session-logs", "", 200},
		{"GET", "/api/agents/coder/runs/9/logs", "", 200},
		{"GET", "/api/agents/other/runs/9/logs", "", 404},
		{"GET", "/api/agents/coder/runs/bad/logs", "", 400},
		{"GET", "/api/agents/coder/runs/99/logs", "", 404},
		{"POST", "/api/agents/coder/runs/9/logs", `{"kind":"blocker","body":"Need access","session_id":"session"}`, 200},
		{"POST", "/api/agents/coder/runs/9/logs", `{"kind":" ","body":"body"}`, 400},
		{"POST", "/api/agents/coder/runs/9/logs", `{`, 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var result any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	if raw.calls != 1 || raw.input.JobID != 9 || raw.input.SessionID != "session" {
		t.Fatalf("POST attribution %+v", raw.input)
	}
	foreign, _ := app.WithPermissions(raw, app.Actor{Role: app.Agent, Name: "other"})
	req := httptest.NewRequest("POST", "/api/agents/coder/runs/9/logs", strings.NewReader(`{"kind":"question","body":"body"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	web.Handler(foreign, nil).ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("foreign agent append %d %s", w.Code, w.Body.String())
	}
	_, err := foreign.(app.SessionLogRepository).AppendSessionLog(context.Background(), agent.SessionLogInput{JobID: 9, Agent: "coder", Kind: "question", Body: "body"})
	if !errors.Is(err, app.ErrForbidden) {
		t.Fatal(err)
	}
}
