package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/task"
)

type detailPayloads struct {
	backfillTestPayloads
	batches  int
	gets     int
	keys     []string
	maxBytes int64
}

func (p *detailPayloads) GetManyBounded(ctx context.Context, keys []string, maxBytes int64) (map[string][]byte, error) {
	p.maxBytes = maxBytes
	rows, err := p.GetMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	var used int64
	for _, value := range rows {
		used += int64(len(value))
	}
	if used > maxBytes {
		return nil, ErrTaskPayloadLimit
	}
	return rows, nil
}

func TestTaskPayloadDetailPageCursorsAndSessionBound(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	item, err := s.CreateTask(ctx, "Page", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO agents(name,description) SELECT 'reader_'||i,'Read' FROM generate_series(1,205) i`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id) SELECT $1,name,'run' FROM agents WHERE name LIKE 'reader_%'`, item.ID); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for i := 0; i < 205; i++ {
		var id int64
		if err := s.Pool.QueryRow(ctx, `INSERT INTO session_logs(job_id,task_id,agent_name,session_id,kind,body) VALUES(1,$1,'reader_1','run','note','Log text') RETURNING id`, item.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	first, sessions, _, err := s.TaskPayloadDetailPage(ctx, item.ID, 0, 0, 1, 11)
	if err != nil || len(first.SessionLogs) != 11 || len(sessions) != 200 {
		t.Fatalf("first page: logs %d sessions %d error %v", len(first.SessionLogs), len(sessions), err)
	}
	cursor := first.SessionLogs[0].ID
	second, _, _, err := s.TaskPayloadDetailPage(ctx, item.ID, 0, cursor, 1, 7)
	if err != nil || len(second.SessionLogs) != 7 || second.SessionLogs[6].ID >= cursor {
		t.Fatalf("second page: %+v %v", second.SessionLogs, err)
	}
	if second.SessionLogs[0].ID != ids[187] {
		t.Fatal("log cursor skipped records")
	}
	max, _, _, err := s.TaskPayloadDetailPage(ctx, item.ID, 0, 0, 999, 999)
	if err != nil || len(max.SessionLogs) != 201 {
		t.Fatalf("maximum page: %d %v", len(max.SessionLogs), err)
	}
	if _, _, _, err := s.TaskPayloadDetailPage(ctx, item.ID, 0, -1, 1, 1); err == nil {
		t.Fatal("negative log cursor was accepted")
	}
}

func TestDetailPayloadByteBudget(t *testing.T) {
	p := &detailPayloads{}
	s := &Store{payloads: p}
	ctx := context.Background()
	large := strings.Repeat("x", int(TaskPayloadMaxBytes/2)+1)
	ref, err := s.externalize(ctx, large)
	if err != nil {
		t.Fatal(err)
	}
	one, two := ref, ref
	if err := s.hydrateDetailStrings(ctx, []*string{&one, &two}); !errors.Is(err, ErrTaskPayloadLimit) {
		t.Fatalf("repeated payload byte limit: %v", err)
	}
	if one != ref || two != ref {
		t.Fatal("failed bounded read changed values")
	}
	inline := strings.Repeat("x", int(TaskPayloadMaxBytes)+1)
	if err := s.hydrateDetailStrings(ctx, []*string{&inline}); !errors.Is(err, ErrTaskPayloadLimit) {
		t.Fatalf("inline byte limit: %v", err)
	}
	if err := s.hydrateDetailStrings(ctx, []*string{&one}); err != nil || one != large {
		t.Fatalf("bounded payload read: %v", err)
	}
}

func (p *detailPayloads) Get(ctx context.Context, key string) ([]byte, error) {
	p.gets++
	return p.backfillTestPayloads.Get(ctx, key)
}
func (p *detailPayloads) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	p.batches++
	p.keys = append([]string{}, keys...)
	out := map[string][]byte{}
	for _, key := range keys {
		value, err := p.backfillTestPayloads.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func TestMetadataReadsDoNotReadPayloads(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	item, err := s.CreateTask(ctx, "Metadata", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &detailPayloads{}
	s.SetPayloadStore(p)
	ref, err := s.externalize(ctx, "Run text")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id) VALUES($1,'queue_test','one');`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO agent_session_runs(job_id,task_id,agent_name,session_id,started_at,outcome,summary,summary_status,error) VALUES(100,$1,'queue_test','one',now(),'done',$2,'available',$2)`, item.ID, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,task_id,agent_name,session_id,kind,body) VALUES(100,$1,'queue_test','one','note',$2)`, item.ID, ref); err != nil {
		t.Fatal(err)
	}
	// Remove remote data. Metadata must not require a payload reader.
	s.SetPayloadStore(nil)
	tasks, err := s.TasksMetadata(ctx)
	if err != nil || len(tasks) != 1 || len(tasks[0].SessionLogs) != 0 {
		t.Fatalf("task list: %+v %v", tasks, err)
	}
	one, err := s.TaskMetadata(ctx, item.ID)
	if err != nil || len(one.SessionLogs) != 0 {
		t.Fatalf("task: %+v %v", one, err)
	}
	sessions, err := s.SessionsMetadata(ctx, "")
	if err != nil || len(sessions) != 1 || sessions[0].Summary != "" || sessions[0].Error != "" {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	runs, err := s.SessionHistoryMetadata(ctx, "", item.ID, 0, 1)
	if err != nil || len(runs) != 1 || runs[0].Summary != "" || runs[0].Error != "" || len(runs[0].Logs) != 0 || runs[0].SummaryStatus != "available" {
		t.Fatalf("history: %+v %v", runs, err)
	}
	if _, err := s.Task(ctx, item.ID); err == nil {
		t.Fatal("full task read did not require payloads")
	}
	s.SetPayloadStore(p)
	full, err := s.Task(ctx, item.ID)
	if err != nil || len(full.SessionLogs) != 1 || full.SessionLogs[0].Body != "Run text" {
		t.Fatalf("full task: %+v %v", full, err)
	}
}

func TestTaskPayloadDetailBatchAndBounds(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	item, err := s.CreateTask(ctx, "Detail", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateTask(ctx, "Other", "Other text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &detailPayloads{}
	s.SetPayloadStore(p)
	ref, err := s.externalize(ctx, "Shared text")
	if err != nil {
		t.Fatal(err)
	}
	missing := payloadPrefix + payloadKey([]byte("Missing text"))
	for _, id := range []int64{item.ID, other.ID} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id) VALUES($1,'queue_test','one')`, id); err != nil {
			t.Fatal(err)
		}
	}
	for i := int64(1); i <= 205; i++ {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO agent_session_runs(job_id,task_id,agent_name,session_id,started_at,outcome,summary,summary_status,error) VALUES($1,$2,'queue_test','one',now(),'done',$3,'available',$3)`, i, item.ID, ref); err != nil {
			t.Fatal(err)
		}
		logRef := ref
		if i == 1 {
			logRef = missing
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,task_id,agent_name,session_id,kind,body) VALUES($1,$2,'queue_test','one','note',$3)`, i, item.ID, logRef); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO agent_session_runs(job_id,task_id,agent_name,session_id,started_at,outcome,summary,summary_status) VALUES(999,$1,'queue_test','one',now(),'done',$2,'available')`, other.ID, missing); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		before      int64
		limit, want int
	}{{0, 1, 1}, {0, 0, 50}, {0, 999, 200}, {5, 2, 2}} {
		t.Run(fmt.Sprintf("%d_%d", tc.before, tc.limit), func(t *testing.T) {
			p.batches, p.gets = 0, 0
			detail, sessions, runs, err := s.TaskPayloadDetail(ctx, item.ID, tc.before, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != tc.want || len(detail.SessionLogs) != 200 || len(sessions) != 1 {
				t.Fatalf("counts: %d %d %d", len(runs), len(detail.SessionLogs), len(sessions))
			}
			if p.batches != 1 || p.gets != 0 || len(p.keys) != 1 {
				t.Fatalf("reads: %d %d %v", p.batches, p.gets, p.keys)
			}
			if p.maxBytes != TaskPayloadMaxBytes {
				t.Fatal("detail read did not set the byte limit")
			}
			for i, log := range detail.SessionLogs {
				if log.Body != "Shared text" || (i > 0 && log.ID <= detail.SessionLogs[i-1].ID) {
					t.Fatal("task log text or order is incorrect")
				}
			}
			for _, run := range runs {
				if run.TaskID != item.ID || run.Summary != "Shared text" || run.Error != "Shared text" {
					t.Fatalf("run: %+v", run)
				}
				if tc.before > 0 && run.JobID >= tc.before {
					t.Fatal("cursor was not applied")
				}
			}
		})
	}
	if _, _, runs, err := s.TaskPayloadDetailPage(ctx, item.ID, 0, 0, 999, 1); err != nil || len(runs) != 201 {
		t.Fatalf("run page maximum: %d %v", len(runs), err)
	}
	p.corrupt = true
	if _, _, _, err := s.TaskPayloadDetail(ctx, item.ID, 0, 1); err == nil {
		t.Fatal("corrupt payload was accepted")
	}
	p.corrupt = false
	delete(p.values, payloadKey([]byte("Shared text")))
	if _, _, _, err := s.TaskPayloadDetail(ctx, item.ID, 0, 1); err == nil {
		t.Fatal("missing payload was accepted")
	}
	empty, err := s.CreateTask(ctx, "Empty", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.batches, p.gets = 0, 0
	if _, sessions, runs, err := s.TaskPayloadDetail(ctx, empty.ID, 0, 1); err != nil || len(sessions) != 0 || len(runs) != 0 {
		t.Fatalf("empty detail: %v %v %v", sessions, runs, err)
	}
	if p.batches != 0 || p.gets != 0 {
		t.Fatal("empty detail read remote payloads")
	}
	for _, bounds := range [][2]int64{{0, 0}, {-1, 0}, {item.ID, -1}} {
		if _, _, _, err := s.TaskPayloadDetail(ctx, bounds[0], bounds[1], 1); err == nil {
			t.Fatal("invalid bounds were accepted")
		}
	}
}
