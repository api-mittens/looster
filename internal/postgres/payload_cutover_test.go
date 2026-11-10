package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/task"
)

func TestEnablePayloadCutoverRequiresStore(t *testing.T) {
	var absent *Store
	if err := absent.EnablePayloadCutover(context.Background()); err == nil {
		t.Fatal("nil store accepted")
	}
	if err := (&Store{}).EnablePayloadCutover(context.Background()); err == nil {
		t.Fatal("unconfigured payload store accepted")
	}
}

func TestPayloadCutoverFencesOldWritersAndAllowsUnchangedLegacy(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	job := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "done", []byte{0, 255, 1})
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET summary='old summary',error='old error' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	s.SetPayloadStore(&backfillTestPayloads{})
	if err := s.EnablePayloadCutover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnablePayloadCutover(ctx); err != nil {
		t.Fatalf("not idempotent: %v", err)
	}
	// Old code's metadata-only updates must work even when explicitly assigning
	// the unchanged legacy values, while any changed nonempty value is rejected.
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET session_id='metadata changed',summary=summary,error=error,history=history WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE jobs SET history=decode('00ff','hex') WHERE id=$1`,
		`UPDATE jobs SET summary='new inline summary' WHERE id=$1`,
		`UPDATE jobs SET error='new inline error' WHERE id=$1`,
		`UPDATE jobs SET summary='looster-payload:v1:bad' WHERE id=$1`,
	} {
		if _, err := s.Pool.Exec(ctx, query, job); err == nil {
			t.Fatalf("inline write accepted: %s", query)
		}
	}
	for _, query := range []string{
		`INSERT INTO events(kind,body) VALUES('agent_output','old inline output')`,
		`INSERT INTO events(kind,body) VALUES('worker_error','old inline system log')`,
		`INSERT INTO session_logs(job_id,session_id,agent_name,kind,body) VALUES(99,'session','queue_test','note','old inline log')`,
		`INSERT INTO agent_session_runs(job_id,agent_name,started_at,outcome,summary,summary_status) VALUES(99,'queue_test',now(),'done','old inline summary','available')`,
		`INSERT INTO agent_session_runs(job_id,agent_name,started_at,outcome,error,summary_status) VALUES(99,'queue_test',now(),'failed','old inline error','failed')`,
	} {
		if _, err := s.Pool.Exec(ctx, query); err == nil {
			t.Fatalf("inline insert accepted: %s", query)
		}
	}
	for _, query := range []string{
		`INSERT INTO events(kind,body) VALUES('comment','human comment'),('status_changed','todo'),('agent_stopped','session metadata')`,
		`UPDATE jobs SET error='',history=NULL WHERE id=$1`,
	} {
		var err error
		if strings.Contains(query, "$1") {
			_, err = s.Pool.Exec(ctx, query, job)
		} else {
			_, err = s.Pool.Exec(ctx, query)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	old := &Store{Pool: s.Pool}
	if err := old.RecordSystemEvent(ctx, "worker_error", "inline from an old connection"); err == nil {
		t.Fatal("old writer escaped durable fence")
	}
	// Backfill remains permitted after activation, and verification finds no
	// remaining eligible legacy payloads before any separate cleanup decision.
	migrated, err := s.BackfillPayloads(ctx, 100)
	if err != nil || migrated.Migrated != 1 {
		t.Fatalf("fenced backfill: %+v %v", migrated, err)
	}
	verified, err := s.VerifyPayloads(ctx, 0)
	if err != nil || !verified.Complete || verified.Legacy != 0 {
		t.Fatalf("verify: %+v %v", verified, err)
	}
}

func TestPayloadCutoverDrainsInflightWriters(t *testing.T) {
	ctx, s := manifestTestStore(t)
	s.SetPayloadStore(&backfillTestPayloads{})
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output','in flight legacy')`); err != nil {
		t.Fatal(err)
	}
	// A transaction opened before activation cannot rely on a cached disabled bit.
	before, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer before.Rollback(ctx)
	var disabled bool
	if err := before.QueryRow(ctx, `SELECT NOT enabled FROM payload_cutover`).Scan(&disabled); err != nil || !disabled {
		t.Fatalf("expected disabled fence: %v %v", disabled, err)
	}
	// The in-flight writer's SHARE row lock must prevent cutover from returning.
	short, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err = s.EnablePayloadCutover(short)
	cancel()
	if err == nil {
		t.Fatal("cutover did not drain in-flight writer")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// A canceled activation may finish server-side after returning an error.
	// Repeating the explicit operation is safe and establishes enabled state.
	if err := s.EnablePayloadCutover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := before.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output','late legacy')`); err == nil {
		t.Fatal("old transaction bypassed cutover")
	}
}

func TestPayloadCutoverLeaseAndHydratedReaders(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	p := &backfillTestPayloads{}
	s.SetPayloadStore(p)
	if err := s.EnablePayloadCutover(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "payload cutover task", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	job := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
	l := historyClaim(t, ctx, s)
	defer func() {
		if l != nil {
			l.Close()
		}
	}()
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	output := `{"type":"done","text":"durable result"}`
	p.putError = true
	if err := l.Capture(ctx, app.Output{SessionID: "lake-session", Text: output}); err == nil {
		t.Fatal("capture ignored remote failure")
	}
	p.putError = false
	if err := l.Capture(ctx, app.Output{SessionID: "lake-session", Text: output}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: job, Agent: "queue_test", SessionID: "lake-session", Kind: "note", Body: "durable log"}); err != nil {
		t.Fatal(err)
	}
	history := []byte{0, 1, 255, 42, 10}
	if err := l.Archive(ctx, app.Result{SessionID: "lake-session", History: history}); err != nil {
		t.Fatal(err)
	}
	var storedSummary string
	var storedHistory []byte
	if err := s.Pool.QueryRow(ctx, `SELECT summary,history FROM jobs WHERE id=$1`, job).Scan(&storedSummary, &storedHistory); err != nil {
		t.Fatal(err)
	}
	if !isPayloadReference(storedSummary) || !isPayloadReference(string(storedHistory)) {
		t.Fatal("lease retained inline payloads")
	}
	l.Close()
	l = historyClaim(t, ctx, s)
	if !bytes.Equal(l.Job().History, history) {
		t.Fatalf("archive retry history: %v", l.Job().History)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l = nil
	run, err := s.SessionRun(ctx, job)
	if err != nil || run.Summary != "durable result" || len(run.Logs) != 1 || run.Logs[0].Body != "durable log" {
		t.Fatalf("run: %+v %v", run, err)
	}
	runs, err := s.SessionHistory(ctx, "queue_test", created.ID, 0, 10)
	if err != nil || len(runs) != 1 || runs[0].Summary != "durable result" {
		t.Fatalf("history: %+v %v", runs, err)
	}
	outputs, err := s.SessionRunOutput(ctx, job, 0, 10)
	if err != nil || len(outputs) != 1 || outputs[0].Body != output {
		t.Fatalf("output: %+v %v", outputs, err)
	}
	logs, err := s.TaskSessionLogs(ctx, created.ID)
	if err != nil || len(logs) != 1 || logs[0].Body != "durable log" {
		t.Fatalf("logs: %+v %v", logs, err)
	}
	events, err := s.Logs(ctx, event.Filter{TaskID: created.ID, Kinds: []string{"agent_output"}, Limit: 10})
	if err != nil || len(events) != 1 || events[0].Body != output {
		t.Fatalf("events: %+v %v", events, err)
	}
	sessions, err := s.Sessions(ctx, "queue_test")
	if err != nil || len(sessions) != 1 || sessions[0].Summary != "durable result" {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	detail, err := s.Task(ctx, created.ID)
	if err != nil || len(detail.SessionLogs) != 1 || detail.SessionLogs[0].Body != "durable log" {
		t.Fatalf("task: %+v %v", detail, err)
	}
	// Failures must externalize both the mutable job and immutable run errors.
	failed := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	failedLease := historyClaim(t, ctx, s)
	defer func() {
		if failedLease != nil {
			failedLease.Close()
		}
	}()
	if err := failedLease.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := failedLease.Fail(ctx, errors.New("durable error")); err != nil {
		t.Fatal(err)
	}
	failedLease.Close()
	failedLease = nil
	failure, err := s.SessionRun(ctx, failed)
	if err != nil || failure.Error != "durable error" {
		t.Fatalf("failure: %+v %v", failure, err)
	}
	verified, err := s.VerifyPayloads(ctx, 0)
	if err != nil || !verified.Complete || verified.Legacy != 0 || verified.References < 6 {
		t.Fatalf("verified: %+v %v", verified, err)
	}
}
