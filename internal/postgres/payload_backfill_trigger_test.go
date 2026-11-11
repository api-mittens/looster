package postgres

import (
	"fmt"
	"strings"
	"testing"
)

func TestPayloadBackfillTriggerRejectsMetadataAndWorkflowChanges(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	job := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "done", nil)
	for _, query := range []string{
		`INSERT INTO events(kind,body) VALUES('agent_output','output'),('comment','comment')`,
		fmt.Sprintf(`INSERT INTO session_logs(job_id,session_id,agent_name,kind,body) VALUES(%d,'session','queue_test','note','log')`, job),
		fmt.Sprintf(`INSERT INTO agent_session_runs(job_id,agent_name,session_id,started_at,outcome,summary,summary_status,error) VALUES(%d,'queue_test','session',now(),'done','summary','available','error')`, job),
	} {
		if _, err := s.Pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`UPDATE events SET body='looster-payload:v1:'||encode(sha256(convert_to(body,'UTF8')),'hex'),agent_name='changed' WHERE kind='agent_output'`,
		`UPDATE events SET body='looster-payload:v1:'||encode(sha256(convert_to(body,'UTF8')),'hex') WHERE kind='comment'`,
		`UPDATE events SET body='looster-payload:v1:` + strings.Repeat("0", 64) + `' WHERE kind='agent_output'`,
		`UPDATE session_logs SET body='looster-payload:v1:'||encode(sha256(convert_to(body,'UTF8')),'hex'),kind='changed'`,
		`UPDATE agent_session_runs SET summary='looster-payload:v1:'||encode(sha256(convert_to(summary,'UTF8')),'hex'),outcome='failed'`,
		`UPDATE agent_session_runs SET error='looster-payload:v1:` + strings.Repeat("0", 64) + `'`,
		`DELETE FROM session_logs`,
		`DELETE FROM agent_session_runs`,
		`DELETE FROM events`,
	} {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('looster.payload_backfill','on',true)`); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, query)
		_ = tx.Rollback(ctx)
		if err == nil {
			t.Fatalf("unsafe maintenance accepted: %s", query)
		}
	}
}

func TestBackfillPayloadsSkipsLockedRowsAndResumes(t *testing.T) {
	ctx, s := manifestTestStore(t)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output','locked'),('agent_output','available')`); err != nil {
		t.Fatal(err)
	}
	s.payloads = &backfillTestPayloads{}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM events WHERE body='locked' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	report, err := s.BackfillPayloads(ctx, 10)
	if err != nil || report.Migrated != 1 || report.Complete {
		t.Fatalf("locked batch: %+v %v", report, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	report, err = s.BackfillPayloads(ctx, 10)
	if err != nil || report.Migrated != 1 {
		t.Fatalf("resumed: %+v %v", report, err)
	}
	verified, err := s.VerifyPayloads(ctx, 0)
	if err != nil || !verified.Complete || verified.References != 2 || verified.Legacy != 0 {
		t.Fatalf("verified: %+v %v", verified, err)
	}
}
