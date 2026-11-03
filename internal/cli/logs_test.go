package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
)

type logRepo struct {
	app.Repository
	mu       sync.Mutex
	rows     []event.Event
	requests []event.Filter
	onRead   func(event.Filter)
}

func (r *logRepo) append(e event.Event) { r.mu.Lock(); defer r.mu.Unlock(); r.rows = append(r.rows, e) }
func (r *logRepo) Logs(_ context.Context, f event.Filter) ([]event.Event, error) {
	r.mu.Lock()
	r.requests = append(r.requests, f)
	rows := append([]event.Event{}, r.rows...)
	r.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		if f.Desc {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].ID < rows[j].ID
	})
	out := []event.Event{}
	for _, e := range rows {
		if e.ID <= f.After || (f.Until > 0 && e.ID > f.Until) || (f.TaskID > 0 && (e.TaskID == nil || *e.TaskID != f.TaskID)) {
			continue
		}
		if f.Agent != "" && e.Agent != f.Agent && e.ActorAgent != f.Agent {
			continue
		}
		if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, e.Kind) {
			continue
		}
		if f.Since != nil && e.CreatedAt.Before(*f.Since) {
			continue
		}
		out = append(out, e)
		if len(out) == f.Limit {
			break
		}
	}
	if r.onRead != nil {
		r.onRead(f)
	}
	return out, nil
}
func logFixtures() []event.Event {
	task1, task2 := int64(1), int64(2)
	base := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	rows := []event.Event{
		{ID: 1, Kind: "worker_started", Body: "Worker abc started; agents=coder,reviewer"},
		{ID: 2, Kind: "scheduled", Agent: "delegator", Body: "Inspect sources"},
		{ID: 3, Kind: "agent_started", Agent: "coder", TaskID: &task1},
		{ID: 4, Kind: "comment", ActorAgent: "coder", TaskID: &task1, Body: "Check accessibility"},
		{ID: 5, Kind: "agent_failed", Agent: "reviewer", TaskID: &task2, Body: "Network error"},
		{ID: 6, Kind: "agent_output", Agent: "coder", TaskID: &task1, Body: `{"type":"done","text":"passed\nall"}`},
	}
	for i := range rows {
		rows[i].CreatedAt = base.Add(time.Duration(rows[i].ID) * time.Second)
	}
	return rows
}
func decodedLogIDs(t *testing.T, data []byte) []int64 {
	t.Helper()
	out := []int64{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var e event.Event
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e.ID)
	}
	return out
}
func TestLogSnapshotsAndFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []int64
	}{
		{"recent tail", []string{"--tail", "2"}, []int64{5, 6}},
		{"all", []string{"--tail", "-1"}, []int64{1, 2, 3, 4, 5, 6}},
		{"agent includes actor", []string{"--agent", "coder"}, []int64{3, 4, 6}},
		{"task", []string{"--task", "TSK-1"}, []int64{3, 4, 6}},
		{"standalone scheduled", []string{"--kind", "scheduled"}, []int64{2}},
		{"kinds", []string{"--kind", "scheduled,agent_failed"}, []int64{2, 5}},
		{"after", []string{"--after", "3"}, []int64{4, 5, 6}},
		{"since timestamp", []string{"--since", "2026-10-29T10:00:04Z"}, []int64{4, 5, 6}},
		{"empty tail", []string{"--tail", "0"}, []int64{}},
		{"no matches", []string{"--agent", "unknown"}, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &logRepo{rows: logFixtures()}
			a := &application{repo: repo}
			cmd := a.logsCommand()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(append(tc.args, "--json"))
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := decodedLogIDs(t, out.Bytes()); !slices.Equal(got, tc.want) {
				t.Fatalf("IDs=%v, want %v", got, tc.want)
			}
			if tc.name == "no matches" && !strings.Contains(errOut.String(), "No matching events") {
				t.Fatal("empty result was silent")
			}
		})
	}
}
func TestLogsReplayAllPages(t *testing.T) {
	rows := []event.Event{}
	for id := int64(1); id <= 1201; id++ {
		rows = append(rows, event.Event{ID: id, Kind: "comment"})
	}
	a := &application{repo: &logRepo{rows: rows}}
	cmd := a.logsCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--after", "0", "--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids := decodedLogIDs(t, out.Bytes())
	if len(ids) != 1201 || ids[0] != 1 || ids[1200] != 1201 {
		t.Fatal("replay dropped a page or applied the default tail")
	}
}
func TestLogsFollowAdvancesThroughSparseFilters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	repo := &logRepo{rows: []event.Event{{ID: 1, Agent: "other", Kind: "comment"}}}
	initial := true
	repo.onRead = func(f event.Filter) {
		if initial && f.Desc && f.Until == 0 {
			initial = false
			repo.append(event.Event{ID: 2, Agent: "other", Kind: "comment"})
		}
		if f.Agent == "coder" && !f.Desc && f.Until == 2 {
			repo.append(event.Event{ID: 3, Agent: "coder", Kind: "comment"})
		}
	}
	var out bytes.Buffer
	writer := writeFunc(func(p []byte) (int, error) {
		n, err := out.Write(p)
		if bytes.Contains(p, []byte(`"id":3`)) {
			cancel()
		}
		return n, err
	})
	a := &application{repo: repo}
	cmd := a.logsCommand()
	cmd.SetOut(writer)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--follow", "--tail", "0", "--agent", "coder", "--interval", "1ms", "--json"})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if got := decodedLogIDs(t, out.Bytes()); !slices.Equal(got, []int64{3}) {
		t.Fatalf("missed or duplicated live event: %v", got)
	}
	found := false
	for _, f := range repo.requests {
		if f.Agent == "coder" && f.After == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("cursor did not advance across nonmatching events")
	}
}
func TestLogFormatting(t *testing.T) {
	var out bytes.Buffer
	for _, e := range logFixtures() {
		if err := writeLog(&out, e, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"worker_started", "scheduled agent=delegator", "comment actor=coder task=TSK-1", "done: passed\n  all"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing readable output %q", want)
		}
	}
	if strings.Contains(out.String(), "TSK-0") {
		t.Fatal("standalone event acquired a fake task")
	}
	out.Reset()
	if err := writeLog(&out, event.Event{Kind: "comment", Body: "\x1b[2J"}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("raw terminal escape was rendered")
	}
	err := writeLog(writeFunc(func([]byte) (int, error) { return 0, errors.New("closed output") }), event.Event{}, false)
	if err == nil {
		t.Fatal("write error ignored")
	}
}

func TestLogsFollowKeepsEventsInsertedDuringInitialSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	repo := &logRepo{rows: []event.Event{{ID: 1, Agent: "coder", Kind: "comment"}}}
	initial := true
	repo.onRead = func(f event.Filter) {
		if initial && f.Desc && f.Until == 0 {
			initial = false
			repo.append(event.Event{ID: 2, Agent: "coder", Kind: "comment"})
		}
		if f.Desc && f.Limit == 100 {
			repo.append(event.Event{ID: 3, Agent: "coder", Kind: "comment"})
		}
	}
	var out bytes.Buffer
	writer := writeFunc(func(p []byte) (int, error) {
		n, err := out.Write(p)
		if bytes.Contains(p, []byte(`"id":3`)) {
			cancel()
		}
		return n, err
	})
	a := &application{repo: repo}
	cmd := a.logsCommand()
	cmd.SetOut(writer)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"-f", "--agent", "coder", "--interval", "1ms", "--json"})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if got := decodedLogIDs(t, out.Bytes()); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("snapshot/follow transition lost or duplicated an event: %v", got)
	}
}
func TestInvalidLogFlags(t *testing.T) {
	for _, args := range [][]string{{"--tail", "-2"}, {"--interval", "0s"}, {"--after", "-1"}, {"--after", "1", "--tail", "10"}, {"--task", "bad"}, {"--agent", "../bad"}, {"--since", "not-a-time"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := &logRepo{}
			a := &application{repo: r}
			cmd := a.logsCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("invalid flags accepted")
			}
			if len(r.requests) > 0 {
				t.Fatal("invalid query reached persistence")
			}
		})
	}
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	since, err := logSince("2h", now)
	if err != nil || !since.Equal(now.Add(-2*time.Hour)) {
		t.Fatal("relative time filter incorrect")
	}
}

type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }
