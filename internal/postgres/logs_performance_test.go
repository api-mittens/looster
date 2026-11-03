package postgres

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/event"
)

func TestLogQueryParameterizesAllShapes(t *testing.T) {
	since := time.Now()
	for mask := 0; mask < 64; mask++ {
		f := event.Filter{After: 3, Limit: 17, Desc: mask&32 != 0}
		if mask&1 != 0 {
			f.Until = 100
		}
		if mask&2 != 0 {
			f.TaskID = 42
		}
		if mask&4 != 0 {
			f.Agent = "' OR true --"
		}
		if mask&8 != 0 {
			f.Kinds = []string{"comment", "agent_output"}
		}
		if mask&16 != 0 {
			f.Since = &since
		}
		q, args := logQuery(f)
		if strings.Contains(q, f.Agent) && f.Agent != "" {
			t.Fatal("agent interpolated into SQL")
		}
		for i := range args {
			if !strings.Contains(q, fmt.Sprintf("$%d", i+1)) {
				t.Fatalf("missing parameter %d in %s", i+1, q)
			}
		}
		if strings.Contains(q, " OR ") != (f.Agent != "") {
			t.Fatalf("unexpected optional OR: %s", q)
		}
		if args[0] != f.After || args[len(args)-1] != f.Limit {
			t.Fatalf("bounds: %v", args)
		}
	}
}

func TestLogsSelectedFiltersMatchSemantics(t *testing.T) {
	ctx, s := manifestTestStore(t)
	var first, second int64
	for _, id := range []*int64{&first, &second} {
		if err := s.Pool.QueryRow(ctx, `INSERT INTO tasks(name,description) VALUES('log fixture','') RETURNING id`).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 24; i++ {
		taskID := first
		if i%3 == 0 {
			taskID = second
		}
		kind, agent, actor := "comment", "coder", ""
		if i%2 == 0 {
			kind = "agent_output"
		}
		if i%4 == 0 {
			agent, actor = "reviewer", "coder"
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name,actor_agent,body,created_at) VALUES($1,$2,$3,$4,$5,$6)`, taskID, kind, agent, actor, fmt.Sprint(i), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Logs(ctx, event.Filter{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	since := now.Add(10 * time.Second)
	for mask := 0; mask < 128; mask++ {
		f := event.Filter{Limit: 5, Desc: mask&64 != 0}
		if mask&1 != 0 {
			f.After = all[3].ID
		}
		if mask&2 != 0 {
			f.Until = all[len(all)-3].ID
		}
		if mask&4 != 0 {
			f.TaskID = first
		}
		if mask&8 != 0 {
			f.Agent = "coder"
		}
		if mask&16 != 0 {
			f.Kinds = []string{"comment"}
		}
		if mask&32 != 0 {
			f.Since = &since
		}
		want := []int64{}
		for n := 0; n < len(all); n++ {
			i := n
			if f.Desc {
				i = len(all) - 1 - n
			}
			e := all[i]
			if e.ID <= f.After || (f.Until != 0 && e.ID > f.Until) || (f.TaskID != 0 && (e.TaskID == nil || *e.TaskID != f.TaskID)) || (f.Agent != "" && e.Agent != f.Agent && e.ActorAgent != f.Agent) || (len(f.Kinds) > 0 && e.Kind != "comment") || (f.Since != nil && e.CreatedAt.Before(*f.Since)) {
				continue
			}
			want = append(want, e.ID)
			if len(want) == f.Limit {
				break
			}
		}
		got, err := s.Logs(ctx, f)
		if err != nil {
			t.Fatalf("shape %d: %v", mask, err)
		}
		ids := []int64{}
		for _, e := range got {
			ids = append(ids, e.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("shape %d: got %v want %v", mask, ids, want)
		}
	}
}
