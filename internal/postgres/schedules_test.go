package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
)

func TestRunScheduleRejectsAgentAndInvalidIDBeforeDatabase(t *testing.T) {
	s := &Store{}
	if _, err := s.RunSchedule(event.WithActor(context.Background(), "coder"), 1); !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("agent error: %v", err)
	}
	for _, id := range []int64{0, -1} {
		if _, err := s.RunSchedule(context.Background(), id); err == nil {
			t.Fatal("accepted invalid ID")
		}
	}
}

func TestRunScheduleDurableIdempotentAndCadenceUnchanged(t *testing.T) {
	ctx, s := manifestTestStore(t)
	const source = `AGENT coder WITH "Implement"; ON TODO PING coder; EVERY 1 HOURS PING coder WITH "Manual tick";`
	if _, err := s.ApplyManifest(ctx, source); err != nil {
		t.Fatal(err)
	}
	rules, err := s.Behaviour(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id, other int64
	for _, r := range rules {
		if r.Trigger == "every" {
			id = r.ID
		}
		if r.Trigger == "todo" {
			other = r.ID
		}
	}
	if id < 1 || other < 1 {
		t.Fatalf("missing IDs: %+v", rules)
	}
	var before time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT next_run_at FROM rules WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan app.ScheduleRunResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.RunSchedule(ctx, id); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	queued := 0
	for r := range results {
		if r.RuleID != id || r.AgentName != "coder" {
			t.Fatalf("bad result: %+v", r)
		}
		if r.Status == "queued" {
			queued++
		} else if r.Status != "already_active" {
			t.Fatalf("bad status: %+v", r)
		}
	}
	if queued != 1 {
		t.Fatalf("queued %d times", queued)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE schedule_rule_id=$1 AND task_id IS NULL AND context_task_id IS NULL AND prompt='Manual tick'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("standalone jobs %d: %v", count, err)
	}
	for _, state := range []string{"running", "archiving"} {
		if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET state=$1 WHERE schedule_rule_id=$2`, state, id); err != nil {
			t.Fatal(err)
		}
		r, err := s.RunSchedule(ctx, id)
		if err != nil || r.Status != "already_active" {
			t.Fatalf("%s: %+v %v", state, r, err)
		}
	}
	var after time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT next_run_at FROM rules WHERE id=$1`, id).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatalf("cadence changed: %v %v %v", before, after, err)
	}
	versions, err := s.Manifests(ctx)
	if err != nil || versions[0].Source != source {
		t.Fatalf("manifest changed: %+v %v", versions, err)
	}
	for _, invalid := range []int64{other, id + 9999} {
		if _, err := s.RunSchedule(ctx, invalid); !errors.Is(err, app.ErrNotFound) {
			t.Fatalf("rule %d: %v", invalid, err)
		}
	}
	if _, err := s.ApplyManifest(ctx, "-- identical\n"+source); err != nil {
		t.Fatal(err)
	}
	rules, err = s.Behaviour(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Trigger == "every" && r.ID != id {
			t.Fatal("stable ID changed")
		}
	}
	_, otherStore := manifestTestStore(t)
	if _, err := otherStore.RunSchedule(ctx, id); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("namespace leak: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET state='done' WHERE schedule_rule_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if r, err := s.RunSchedule(ctx, id); err != nil || r.Status != "queued" {
		t.Fatalf("terminal retry: %+v %v", r, err)
	}
}
