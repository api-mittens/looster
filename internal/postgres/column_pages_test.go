package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type columnQueryTrace struct {
	mu      sync.Mutex
	queries []pgx.TraceQueryStartData
}

func (r *columnQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, data)
	return ctx
}
func (*columnQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestColumnTasksReadsOnlySelectedSignalMetadata(t *testing.T) {
	ctx, s := manifestTestStore(t)
	for i := 0; i < 31; i++ {
		if _, err := s.CreateTask(ctx, fmt.Sprint("Task ", i), "", task.Todo, nil); err != nil {
			t.Fatal(err)
		}
	}
	trace := &columnQueryTrace{}
	cfg := s.Pool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	paged := &Store{Pool: pool}
	page, err := paged.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 15 || page.Total != 31 {
		t.Fatalf("unexpected page: %+v", page)
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	found := false
	for _, query := range trace.queries {
		if strings.Contains(query.SQL, "SELECT DISTINCT ON (task_id,kind)") {
			found = true
			if !strings.Contains(query.SQL, "kind,''::text,created_at") || !strings.Contains(query.SQL, "kind IN ('blocker','question')") {
				t.Fatal("signal query reads log bodies or other log kinds")
			}
			ids, ok := query.Args[0].([]int64)
			if !ok || len(ids) != 15 {
				t.Fatalf("unbounded log hydration parameters: %+v", query.Args)
			}
			for i, id := range ids {
				if id != page.Tasks[i].ID {
					t.Fatalf("off-page log hydration: %v", ids)
				}
			}
		}
		if strings.Contains(query.SQL, "SELECT p.id,p.name,p.description") && !strings.Contains(query.SQL, "LIMIT 15 OFFSET $5") {
			t.Fatalf("task hydration missing SQL bound: %s", query.SQL)
		}
	}
	if !found {
		t.Fatal("bounded log hydration query was not observed")
	}
}

func TestColumnTasksNeedsNoPayloadReader(t *testing.T) {
	ctx, s := manifestTestStore(t)
	item, err := s.CreateTask(ctx, "Signals", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := payloadPrefix + payloadKey([]byte("Remote text"))
	for _, kind := range []string{"blocker", "blocker", "question", "note"} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body) VALUES(1,'run','coder',$1,$2,$3)`, item.ID, kind, ref); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo"})
	if err != nil || len(page.Tasks) != 1 {
		t.Fatalf("metadata page: %+v %v", page, err)
	}
	logs := page.Tasks[0].SessionLogs
	if len(logs) != 2 {
		t.Fatalf("signal count: %d", len(logs))
	}
	for _, log := range logs {
		if log.Body != "" || log.TaskID != item.ID || (log.Kind != "blocker" && log.Kind != "question") {
			t.Fatalf("signal metadata: %+v", log)
		}
	}
	graph, err := s.TasksMetadata(ctx)
	if err != nil || len(graph) != 1 || len(graph[0].SessionLogs) != 2 {
		t.Fatalf("graph signal metadata: %+v %v", graph, err)
	}
	for i, log := range graph[0].SessionLogs {
		if log.ID != logs[i].ID || log.Body != "" || log.Kind != logs[i].Kind {
			t.Fatalf("graph and column signals do not match: %+v", log)
		}
	}
}

func TestColumnTasksBoundariesSearchAndClamp(t *testing.T) {
	for _, n := range []int{0, 15, 16, 31} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			ids := []int64{}
			at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < n; i++ {
				row, err := s.CreateTask(ctx, fmt.Sprintf("Task %02d", i), fmt.Sprintf("Description %02d", i), task.Todo, nil)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, row.ID)
				// All activity ties so ID ascending determines exact page boundaries.
				if _, err = s.Pool.Exec(ctx, `UPDATE tasks SET created_at=$2 WHERE id=$1`, row.ID, at); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body,created_at) VALUES($1,'run','coder',$1,'note',$2,$3)`, row.ID, fmt.Sprintf("log-%d", row.ID), at.Add(100*365*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			for page := 1; page <= 4; page++ {
				got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Page: page})
				if err != nil {
					t.Fatal(err)
				}
				max := (n + 14) / 15
				if max < 1 {
					max = 1
				}
				wantPage := page
				if wantPage > max {
					wantPage = max
				}
				start := (wantPage - 1) * 15
				end := start + 15
				if end > n {
					end = n
				}
				if got.Total != int64(n) || got.Page != wantPage || got.PageSize != 15 || len(got.Tasks) != end-start {
					t.Fatalf("n=%d requested=%d got=%+v", n, page, got)
				}
				for i, row := range got.Tasks {
					if row.ID != ids[start+i] || len(row.SessionLogs) != 0 {
						t.Fatalf("bounded hydration/order: %+v", row)
					}
				}
			}
			if n == 31 {
				for _, fuzzy := range []bool{false, true} {
					got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Search: "dscrptn30", Fuzzy: fuzzy})
					if err != nil {
						t.Fatal(err)
					}
					if fuzzy && (got.Total != 1 || len(got.Tasks) != 1 || got.Tasks[0].ID != ids[30]) {
						t.Fatalf("off-page fuzzy match: %+v", got)
					}
					if !fuzzy && got.Total != 0 {
						t.Fatalf("List must remain substring search: %+v", got)
					}
				}
				for _, search := range []string{".*", "[", "\\", "(a+)+$"} {
					got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Search: search, Fuzzy: true})
					if err != nil || got.Total != 0 {
						t.Fatalf("regex input must remain literal %q: %+v %v", search, got, err)
					}
				}
				for _, search := range []string{"Task 30", "Description 30", task.ID(ids[30])} {
					got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Page: 3, Search: search})
					if err != nil {
						t.Fatal(err)
					}
					if got.Total != 1 || got.Page != 1 || len(got.Tasks) != 1 || got.Tasks[0].ID != ids[30] {
						t.Fatalf("search beyond first page %q: %+v", search, got)
					}
				}
				if err := s.RemoveTask(ctx, ids[30]); err != nil {
					t.Fatal(err)
				}
				got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Page: 3})
				if err != nil {
					t.Fatal(err)
				}
				if got.Total != 30 || got.Page != 2 || len(got.Tasks) != 15 {
					t.Fatalf("removal clamp: %+v", got)
				}
			}
		})
	}
}

func TestColumnTasksPriorityAgentAndSearchFields(t *testing.T) {
	ctx, s := manifestTestStore(t)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO agents(name,description) VALUES('coder',''),('reviewer',''); INSERT INTO tags(name,description) VALUES('beyond/page','')`); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for i := 0; i < 31; i++ {
		row, err := s.CreateTask(ctx, fmt.Sprintf("Task %d", i), "", task.Planning, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	for _, entry := range []struct {
		index int
		kind  string
	}{{0, "blocker"}, {1, "question"}} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body,created_at) VALUES($1,'run','coder',$1,$2,'historical',now()-interval '1 day')`, ids[entry.index], entry.kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO task_tags(task_id,tag_id) SELECT $1,id FROM tags WHERE name='beyond/page'`, ids[30]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id,status) VALUES($1,'reviewer','run','idle')`, ids[30]); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"planning", "agent:coder"} {
		got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: key, Page: 1, ActiveTaskIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Tasks) != 15 || got.Tasks[0].ID != ids[0] || got.Tasks[1].ID != ids[1] {
			t.Fatalf("priority %s: %+v", key, got)
		}
	}
	for _, search := range []string{"beyond/page", "REVIEWER"} {
		got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "planning", Search: search, Page: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 || got.Tasks[0].ID != ids[30] {
			t.Fatalf("whole dataset search %q: %+v", search, got)
		}
	}
	got, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "agent:coder", ActiveTaskIDs: ids[:16], Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 16 || len(got.Tasks) != 1 {
		t.Fatalf("agent page: %+v", got)
	}
	empty, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "agent:reviewer", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Tasks) != 0 || empty.Page != 1 {
		t.Fatalf("agent isolation: %+v", empty)
	}
}
