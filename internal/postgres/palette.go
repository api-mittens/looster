package postgres

import (
	"context"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

var _ app.PaletteRepository = (*Store)(nil)

// PaletteTasks ranks all stages together before the global bound. The search
// string is exactly searchIndex's task string, including sorted tags and spaces.
// No sessions, counters, activity or log bodies are hydrated for this read port.
func (s *Store) PaletteTasks(ctx context.Context, search string) ([]task.Task, error) {
	rows, err := s.Pool.Query(ctx, `WITH candidates AS MATERIALIZED (
 SELECT t.id,t.name,t.description,t.status,
 ARRAY(SELECT g.name FROM tags g JOIN task_tags tt ON tt.tag_id=g.id WHERE tt.task_id=t.id ORDER BY g.name) AS tags
 FROM tasks t
 ), ranked AS MATERIALIZED (
 SELECT c.*,palette_fuzzy_score($1,'TSK-'||id||' '||name||' '||description||' '||status||' '||array_to_string(tags,' ')) AS score
 FROM candidates c
 ) SELECT id,name,description,status,tags FROM ranked WHERE score>=0
 -- Labels have unique ASCII TSK-<id> prefixes. C ordering of the ID followed
 -- by the label's space matches localeCompare, including prefix IDs (1,10).
 ORDER BY score DESC, ('TSK-'||id||' ') COLLATE "C" LIMIT 50`, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []task.Task{}
	for rows.Next() {
		var t task.Task
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Status, &t.Tags); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
