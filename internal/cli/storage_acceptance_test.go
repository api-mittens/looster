package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/spf13/viper"
)

// Requires exclusively disposable PG/S3 fixtures. Never reads normal database or
// S3 credentials. Uses the real automatic engine/bootstrap and Parquet read path.
func TestStorageAcceptance(t *testing.T) {
	if os.Getenv("LOOSTER_STORAGE_ACCEPTANCE") != "yes" {
		t.Skip("requires explicitly disposable PostgreSQL and S3 fixtures")
	}
	for _, key := range []string{"LOOSTER_TEST_DATABASE_URL", "LOOSTER_TEST_S3_ENDPOINT", "LOOSTER_TEST_S3_BUCKET", "LOOSTER_TEST_S3_ACCESS_KEY", "LOOSTER_TEST_S3_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("provide explicit %s", key)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ns := fmt.Sprintf("storage-%d", time.Now().UnixNano())
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if err := postgres.EnsureNamespace(ctx, url, ns); err != nil {
		t.Fatal(err)
	}
	s, err := postgres.OpenNamespace(ctx, url, ns)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Retain no application rows after the acceptance test. Lake objects are in
	// a unique test namespace and stay confined to the disposable fixture bucket.
	defer func() {
		_, e := s.Pool.Exec(context.Background(), `DROP SCHEMA "looster_ns_`+ns+`" CASCADE`)
		if e != nil {
			t.Errorf("cleanup: %v", e)
		}
	}()
	a := &application{v: viper.New()}
	a.v.Set("database_url", url)
	for _, name := range []string{"endpoint", "bucket", "access_key", "secret_key"} {
		a.v.Set("s3."+name, os.Getenv("LOOSTER_TEST_S3_"+strings.ToUpper(name)))
	}
	a.v.Set("ducklake.spool_dir", t.TempDir())
	p, err := a.openPayloadStore(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s.SetPayloadStore(p)
	if _, err = s.Pool.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output','legacy historical output')`); err != nil {
		t.Fatal(err)
	}
	if err = s.EnablePayloadCutover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyManifest(ctx, `AGENT storage_test WITH "Storage acceptance";`); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "Lake acceptance", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	var job int64
	err = s.Pool.QueryRow(ctx, `WITH e AS (INSERT INTO events(kind,agent_name) VALUES('scheduled','storage_test') RETURNING id) INSERT INTO jobs(task_id,agent_name,event_id,state) SELECT $1,'storage_test',id,'pending' FROM e RETURNING id`, created.ID).Scan(&job)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Claim(ctx, []string{"storage_test"})
	if err != nil || l == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = l.Started(ctx); err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = l.Capture(ctx, app.Output{SessionID: "acceptance-session", Text: `{"type":"done","text":"lake summary"}`}); err != nil {
		l.Close()
		t.Fatal(err)
	}
	history := bytes.Repeat([]byte("\x00archive bytes\xff"), 400000)
	if err = l.Archive(ctx, app.Result{SessionID: "acceptance-session", History: history}); err != nil {
		l.Close()
		t.Fatal(err)
	}
	var stagedBytes int
	if err = s.Pool.QueryRow(ctx, `SELECT octet_length(history) FROM jobs WHERE id=$1`, job).Scan(&stagedBytes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	if stagedBytes != len("looster-payload:v1:")+64 || stagedBytes >= len(history) {
		l.Close()
		t.Fatalf("archive staging did not reduce PG payload bytes: source=%d staged=%d", len(history), stagedBytes)
	}
	t.Logf("archive staging payload reduction: %d source bytes to %d PostgreSQL reference bytes", len(history), stagedBytes)
	l.Close()
	// Different backend owner proves acknowledgment is not only a local overlay.
	otherApp := &application{v: a.v}
	otherApp.v.Set("ducklake.spool_dir", t.TempDir())
	other, err := otherApp.openPayloadStore(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s.SetPayloadStore(other)
	l, err = s.Claim(ctx, []string{"storage_test"})
	if err != nil || l == nil {
		t.Fatalf("archive reclaim: %v", err)
	}
	if !bytes.Equal(l.Job().History, history) {
		l.Close()
		t.Fatal("archive retry lost binary bytes")
	}
	if err = l.Complete(ctx); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	log, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: job, Agent: "storage_test", SessionID: "acceptance-session", Kind: "note", Body: "lake-backed log"})
	if err != nil || log.Body != "lake-backed log" {
		t.Fatalf("append log: %+v %v", log, err)
	}
	run, err := s.SessionRun(ctx, job)
	if err != nil || run.Summary != "lake summary" || len(run.Logs) != 1 || run.Logs[0].Body != "lake-backed log" {
		t.Fatalf("run hydration: %+v %v", run, err)
	}
	output, err := s.SessionRunOutput(ctx, job, 0, 100)
	if err != nil || len(output) != 1 || !strings.Contains(output[0].Body, "lake summary") {
		t.Fatalf("output: %+v %v", output, err)
	}
	logs, err := s.Logs(ctx, event.Filter{TaskID: created.ID, Limit: 100})
	if err != nil || len(logs) == 0 {
		t.Fatalf("logs: %+v %v", logs, err)
	}
	for _, e := range logs {
		if strings.HasPrefix(e.Body, "looster-payload:v1:") {
			t.Fatal("public log leaked reference")
		}
	}
	// PostgreSQL holds only small references, not bulk archive/output bytes.
	var body string
	if err = s.Pool.QueryRow(ctx, `SELECT body FROM events WHERE job_id=$1 AND kind='agent_output'`, job).Scan(&body); err != nil || !strings.HasPrefix(body, "looster-payload:v1:") {
		t.Fatalf("PG payload not externalized: %q %v", body, err)
	}
	before, err := s.VerifyPayloads(ctx, 0)
	if err != nil || before.Legacy != 1 {
		t.Fatalf("historical baseline: %+v %v", before, err)
	}
	migrated, err := s.BackfillPayloads(ctx, 1000)
	if err != nil || migrated.Migrated != 1 {
		t.Fatalf("real historical backfill: %+v %v", migrated, err)
	}
	verified, err := s.VerifyPayloads(ctx, 0)
	if err != nil || !verified.Complete || verified.Legacy != 0 || verified.References == 0 {
		t.Fatalf("reread reconciliation: %+v %v", verified, err)
	}
	if verified.CleanupReady {
		t.Fatal("destructive cleanup must remain gated")
	}
}
