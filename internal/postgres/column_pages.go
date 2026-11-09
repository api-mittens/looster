package postgres

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5"
)

var _ app.ColumnRepository = (*Store)(nil)

// ColumnTasks selects IDs before hydrating tags, counters and log bodies. Count
// and page share a repeatable-read snapshot so deletion cannot yield a stale
// empty last page. Only fifteen tasks' log bodies ever leave PostgreSQL.
func (s *Store) ColumnTasks(ctx context.Context, q task.ColumnQuery) (task.ColumnPage, error) {
	out := task.ColumnPage{Key: q.Key, Tasks: []task.Task{}, Page: q.Page, PageSize: task.ColumnPageSize}
	agentColumn := strings.HasPrefix(q.Key, "agent:")
	if !agentColumn {
		if err := task.Status(q.Key).Validate(); err != nil {
			return out, err
		}
	} else if strings.TrimPrefix(q.Key, "agent:") == "" {
		return out, fmt.Errorf("empty agent column")
	}
	if out.Page < 1 {
		out.Page = 1
	}
	ids := q.ActiveTaskIDs
	if ids == nil {
		ids = []int64{}
	}
	haystack := `lower(concat_ws(' ',t.name,t.description,t.status,'TSK-'||t.id,
 (SELECT string_agg(g.name,' ') FROM tags g JOIN task_tags tt ON tt.tag_id=g.id WHERE tt.task_id=t.id),
 (SELECT string_agg(s.agent_name,' ') FROM sessions s WHERE s.task_id=t.id)))`
	search := strings.ToLower(strings.TrimSpace(q.Search))
	match := "strpos(" + haystack + ",$4)>0"
	if q.Fuzzy {
		haystack = strings.Replace(haystack, "t.name,t.description,t.status,'TSK-'||t.id", "'TSK-'||t.id,t.name,t.description,t.status", 1)
		// Quote each character before joining so user input cannot introduce
		// regex operators, repetitions or pathological backtracking patterns.
		characters := []string{}
		for _, character := range search {
			characters = append(characters, regexp.QuoteMeta(string(character)))
		}
		search = strings.Join(characters, ".*")
		match = haystack + " ~ $4"
	}
	filter := ` FROM tasks t WHERE (($1::boolean AND t.id=ANY($2::bigint[])) OR (NOT $1 AND t.status=$3))
 AND ($4='' OR ` + match + `)`
	args := []any{agentColumn, ids, q.Key, search}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT count(*)`+filter, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	maxPage := int((out.Total + task.ColumnPageSize - 1) / task.ColumnPageSize)
	if maxPage < 1 {
		maxPage = 1
	}
	if out.Page > maxPage {
		out.Page = maxPage
	}
	args = append(args, (out.Page-1)*task.ColumnPageSize, agentColumn || q.Key == "planning" || q.Key == "todo")
	rows, err := tx.Query(ctx, `WITH page AS (
 SELECT t.*,GREATEST(t.created_at,
 (SELECT max(e.created_at) FROM events e WHERE e.task_id=t.id),
 (SELECT max(l.created_at) FROM session_logs l WHERE l.task_id=t.id)) AS activity,
 CASE WHEN $6::boolean THEN CASE
 WHEN EXISTS(SELECT FROM session_logs l WHERE l.task_id=t.id AND l.kind='blocker') THEN 2
 WHEN EXISTS(SELECT FROM session_logs l WHERE l.task_id=t.id AND l.kind='question') THEN 1 ELSE 0 END ELSE 0 END AS priority
 `+filter+` ORDER BY priority DESC,activity DESC,t.id LIMIT 15 OFFSET $5
 ) SELECT p.id,p.name,p.description,p.status,p.created_at,p.activity,
 ARRAY(SELECT g.name FROM tags g JOIN task_tags tt ON tt.tag_id=g.id WHERE tt.task_id=p.id ORDER BY g.name),
 (SELECT count(*) FROM events e WHERE e.task_id=p.id AND e.kind='status_changed'),
 (SELECT count(*) FROM events e WHERE e.task_id=p.id AND e.kind='comment')
 FROM page p ORDER BY p.priority DESC,p.activity DESC,p.id`, args...)
	if err != nil {
		return out, err
	}
	pageIDs := []int64{}
	for rows.Next() {
		var t task.Task
		if err = rows.Scan(&t.ID, &t.Name, &t.Description, &t.Status, &t.CreatedAt, &t.LastActivityAt, &t.Tags, &t.StatusChanges, &t.CommentCount); err != nil {
			rows.Close()
			return out, err
		}
		out.Tasks = append(out.Tasks, t)
		pageIDs = append(pageIDs, t.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Use the same snapshot for bounded log hydration, including empty pages.
	if len(pageIDs) > 0 {
		logs, err := tx.Query(ctx, `SELECT `+sessionLogColumns+` FROM session_logs WHERE task_id=ANY($1) ORDER BY id`, pageIDs)
		if err != nil {
			return out, err
		}
		positions := map[int64]int{}
		for i := range out.Tasks {
			positions[out.Tasks[i].ID] = i
		}
		for logs.Next() {
			l, e := scanSessionLog(logs)
			if e != nil {
				logs.Close()
				return out, e
			}
			i := positions[l.TaskID]
			out.Tasks[i].SessionLogs = append(out.Tasks[i].SessionLogs, l)
		}
		err = logs.Err()
		logs.Close()
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
