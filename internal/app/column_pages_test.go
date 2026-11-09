package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

type columnPermissionRepo struct {
	Repository
	query   task.ColumnQuery
	failure error
}

func (*columnPermissionRepo) Tasks(context.Context) ([]task.Task, error) {
	panic("bounded List must not call Tasks")
}
func (r *columnPermissionRepo) ColumnTasks(_ context.Context, q task.ColumnQuery) (task.ColumnPage, error) {
	r.query = q
	return task.ColumnPage{Key: q.Key, Page: q.Page, PageSize: 15, Total: 31, Tasks: []task.Task{{ID: 9}}}, r.failure
}
func (r *columnPermissionRepo) BoardSessions(context.Context) ([]agent.Session, error) {
	return []agent.Session{{TaskID: 9, Agent: "coder", Status: "running"}}, r.failure
}

func TestPermissionColumnPaginationForwarding(t *testing.T) {
	for _, role := range []Role{Human, Agent} {
		t.Run(string(role), func(t *testing.T) {
			raw := &columnPermissionRepo{}
			wrapped, err := WithPermissions(raw, Actor{Role: role, Name: "coder"})
			if err != nil {
				t.Fatal(err)
			}
			columns, ok := wrapped.(ColumnRepository)
			if !ok {
				t.Fatal("permission wrapper dropped column pagination")
			}
			query := task.ColumnQuery{Key: "agent:coder", Search: "remote task", Page: 3, ActiveTaskIDs: []int64{9, 20}}
			page, err := columns.ColumnTasks(context.Background(), query)
			if err != nil || !reflect.DeepEqual(raw.query, query) || page.Key != query.Key || page.Page != 3 || page.Total != 31 || len(page.Tasks) != 1 {
				t.Fatalf("page forwarding: %+v %+v %v", raw.query, page, err)
			}
			sessions, err := columns.BoardSessions(context.Background())
			if err != nil || len(sessions) != 1 || sessions[0].Agent != "coder" {
				t.Fatalf("metadata forwarding: %+v %v", sessions, err)
			}
			raw.failure = errors.New("storage failure")
			if _, err := columns.ColumnTasks(context.Background(), query); !errors.Is(err, raw.failure) {
				t.Fatal(err)
			}
			if _, err := columns.BoardSessions(context.Background()); !errors.Is(err, raw.failure) {
				t.Fatal(err)
			}
		})
	}
}
func TestPermissionColumnPaginationUnavailable(t *testing.T) {
	wrapped, err := WithPermissions(struct{ Repository }{}, Actor{Role: Human})
	if err != nil {
		t.Fatal(err)
	}
	columns := wrapped.(ColumnRepository)
	if _, err := columns.ColumnTasks(context.Background(), task.ColumnQuery{Key: "todo", Page: 1}); !errors.Is(err, ErrColumnPaginationUnavailable) {
		t.Fatal(err)
	}
	if _, err := columns.BoardSessions(context.Background()); !errors.Is(err, ErrColumnPaginationUnavailable) {
		t.Fatal(err)
	}
}
