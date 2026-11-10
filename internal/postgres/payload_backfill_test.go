package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type backfillTestPayloads struct {
	values   map[string][]byte
	corrupt  bool
	putError bool
}

func (p *backfillTestPayloads) Put(_ context.Context, key string, value []byte) error {
	if p.putError {
		return errors.New("remote unavailable")
	}
	if p.values == nil {
		p.values = map[string][]byte{}
	}
	p.values[key] = bytes.Clone(value)
	return nil
}
func (p *backfillTestPayloads) Get(_ context.Context, key string) ([]byte, error) {
	if p.corrupt {
		return []byte("corrupt remote data"), nil
	}
	value, ok := p.values[key]
	if !ok {
		return nil, errors.New("missing payload")
	}
	return bytes.Clone(value), nil
}

func TestBackfillPayloadReference(t *testing.T) {
	value := []byte("payload\x00\xff")
	key := backfillKey(value)
	if len(key) != 64 {
		t.Fatal(key)
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{backfillPayloadPrefix + key, true},
		{backfillPayloadPrefix + strings.ToUpper(key), false},
		{backfillPayloadPrefix + "abc", false},
		{backfillPayloadPrefix + strings.Repeat("z", 64), false},
		{"ordinary text", false},
	} {
		got, valid := backfillReference([]byte(tc.value))
		if valid != tc.valid || valid && got != key {
			t.Fatalf("reference %q: %q %v", tc.value, got, valid)
		}
	}
}

func TestBackfillPayloadsRequiresConfigurationAndBound(t *testing.T) {
	s := &Store{}
	if _, err := s.BackfillPayloads(context.Background(), 10); err == nil {
		t.Fatal("missing payload store accepted")
	}
	if _, err := s.VerifyPayloads(context.Background(), 0); err == nil {
		t.Fatal("missing payload store accepted")
	}
	s.payloads = &backfillTestPayloads{}
	for _, limit := range []int{0, -1} {
		if _, err := s.BackfillPayloads(context.Background(), limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err := s.VerifyPayloads(context.Background(), -1); err == nil {
		t.Fatal("invalid verification limit accepted")
	}
}

func TestBackfillPayloadsResumesAndPreservesMetadata(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	p := &backfillTestPayloads{}
	job := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "archiving", []byte{0, 1, 255, 42})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE jobs SET summary='job summary',error='job error' WHERE id=$1`, job)
	for _, kind := range []string{"agent_output", "system", "worker_started", "worker_stopped", "worker_error", "worker_start_failed", "scheduler_failed", "scheduler_dispatched", "comment", "status_changed", "scheduled"} {
		exec(`INSERT INTO events(kind,body) VALUES($1,$2)`, kind, "body "+kind)
	}
	exec(`INSERT INTO session_logs(job_id,session_id,agent_name,kind,body) VALUES($1,'session','queue_test','note','log body')`, job)
	exec(`INSERT INTO agent_session_runs(job_id,agent_name,session_id,started_at,outcome,summary,summary_status,error) VALUES($1,'queue_test','session',now(),'failed','run summary','available','run error')`, job)
	s.payloads = p
	before, err := s.VerifyPayloads(ctx, 0)
	if err != nil || before.Legacy != 14 || !before.Complete {
		t.Fatalf("before: %+v %v", before, err)
	}
	total := 0
	for i := 0; i < 20; i++ {
		report, err := s.BackfillPayloads(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		if report.Migrated > 2 || report.CleanupReady || !report.WriterFenceRequired {
			t.Fatalf("unsafe report: %+v", report)
		}
		total += report.Migrated
		if report.Migrated == 0 {
			break
		}
	}
	if total != 14 {
		t.Fatalf("migrated=%d", total)
	}
	report, err := s.VerifyPayloads(ctx, 0)
	if err != nil || !report.Complete || report.Legacy != 0 || report.References != 14 || report.CleanupReady {
		t.Fatalf("verify: %+v %v", report, err)
	}
	bounded, err := s.VerifyPayloads(ctx, 1)
	if err != nil || bounded.Complete || bounded.Scanned != 1 {
		t.Fatalf("bounded verify: %+v %v", bounded, err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE kind IN ('comment','status_changed','scheduled') AND body LIKE 'body %'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("workflow bodies changed: %d %v", count, err)
	}
	var history []byte
	if err := s.Pool.QueryRow(ctx, `SELECT history FROM jobs WHERE id=$1`, job).Scan(&history); err != nil {
		t.Fatal(err)
	}
	key, ok := backfillReference(history)
	if !ok || !bytes.Equal(p.values[key], []byte{0, 1, 255, 42}) {
		t.Fatal("binary history changed")
	}
	// The maintenance setting is transaction-local. Append-only invariants remain.
	for _, query := range []string{
		`UPDATE session_logs SET body='changed'`,
		`DELETE FROM session_logs`,
		`UPDATE agent_session_runs SET summary='changed'`,
		`DELETE FROM agent_session_runs`,
		`UPDATE events SET body='changed'`,
	} {
		if _, err := s.Pool.Exec(ctx, query); err == nil {
			t.Fatalf("immutability lost: %s", query)
		}
	}
}

func TestBackfillPayloadsDoesNotReplaceUnverifiedBytes(t *testing.T) {
	for _, mode := range []string{"corrupt", "put failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			if _, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output','retain this')`); err != nil {
				t.Fatal(err)
			}
			p := &backfillTestPayloads{corrupt: mode == "corrupt", putError: mode == "put failure"}
			s.payloads = p
			report, err := s.BackfillPayloads(ctx, 10)
			if err == nil || report.Migrated != 0 {
				t.Fatalf("failed reread allowed migration: %+v %v", report, err)
			}
			var body string
			if err := s.Pool.QueryRow(ctx, `SELECT body FROM events WHERE kind='agent_output'`).Scan(&body); err != nil || body != "retain this" {
				t.Fatalf("legacy lost: %q %v", body, err)
			}
			p.corrupt = false
			p.putError = false
			if report, err := s.BackfillPayloads(ctx, 10); err != nil || report.Migrated != 1 {
				t.Fatalf("retry: %+v %v", report, err)
			}
			p.corrupt = true
			if _, err := s.VerifyPayloads(ctx, 0); err == nil {
				t.Fatal("corrupt remote verified")
			}
		})
	}
}

func TestVerifyPayloadsRejectsMalformedAndMissingReferences(t *testing.T) {
	for _, ref := range []string{backfillPayloadPrefix + "invalid", backfillPayloadPrefix + backfillKey([]byte("missing"))} {
		t.Run(ref, func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			if _, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,body) VALUES('agent_output',$1)`, ref); err != nil {
				t.Fatal(err)
			}
			s.payloads = &backfillTestPayloads{}
			if _, err := s.VerifyPayloads(ctx, 0); err == nil {
				t.Fatal("invalid reference verified")
			}
		})
	}
}
