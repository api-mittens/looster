package postgres

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/api-mittens/looster/internal/task"
)

func paletteNode(t *testing.T, script string, input any, output any) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for JS/SQL parity: ", err)
	}
	path, err := filepath.Abs("../web/static/search.mjs")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "-e", `import {fuzzyScore,searchItems} from `+fmt.Sprintf("%q", "file://"+path)+`; const input=JSON.parse(process.argv[1]); `+script, string(data))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, raw)
	}
	if err = json.Unmarshal(raw, output); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func TestPaletteSQLScoreParity(t *testing.T) {
	ctx, s := manifestTestStore(t)
	pairs := [][2]string{{"", "anything"}, {"  ", ""}, {"xyz", "example"}, {"design", "design"}, {"design", "A design task"}, {"aa", "a___a"}, {"ab", "a/b"}, {"ab", "a\nb"}, {"[.*", "[.*"}, {"😀b", "😀b"}, {"b", "😀 b"}, {"tsk1", "TSK-123 name"}, {"é", "École"}, {"σ", "ΟΣ"}, {"i\u0307", "İ"}, {"\uFEFF x \uFEFF", "X"}, {"ab", "a\u2003b"}, {"a", string(make([]byte, 0))}}
	// Deterministic generated cases exercise greedy first occurrence, punctuation,
	// whitespace, repeated characters, and UTF-16 positions without a copied scorer.
	values := []string{"alpha beta", "a-a a", "a/b_c.d:e#f-g", "😀alpha😀beta", "École Σ", "foo\tbar\nxyz"}
	for _, value := range values {
		for _, q := range []string{"a", "ab", "aa", "fb", "xyz", "😀b", "Σ", "é", "\t", "notfound"} {
			pairs = append(pairs, [2]string{q, value})
		}
	}
	var want []float64
	for _, space := range []rune{'\t', '\n', '\r', '\v', '\f', 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff} {
		pairs = append(pairs, [2]string{string(space) + "ab" + string(space), "a" + string(space) + "b"})
	}
	paletteNode(t, `console.log(JSON.stringify(input.map(([q,v])=>fuzzyScore(q,v))));`, pairs, &want)
	for i, p := range pairs {
		var got float64
		if err := s.Pool.QueryRow(ctx, `SELECT palette_fuzzy_score($1,$2)`, p[0], p[1]).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-want[i]) > 1e-10 {
			t.Errorf("%q / %q SQL=%v JS=%v", p[0], p[1], got, want[i])
		}
	}
}

func TestPaletteGlobalRankingBeforeLimit(t *testing.T) {
	ctx, s := manifestTestStore(t)
	exact, err := s.CreateTask(ctx, "needle", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Newer, fuzzy matches in the SAME stage must not crowd out either the exact
	// match or the global top fifty. Ordinary column ordering still returns these.
	_, err = s.Pool.Exec(ctx, `INSERT INTO tasks(name,description,status,created_at)
 SELECT 'nxexexdxlxe '||i,'newer fuzzy','todo',now()+interval '1 day' FROM generate_series(1,90) i;
 INSERT INTO tasks(name,description,status) VALUES ('needle other with a much longer task title','', 'closed'),('unrelated','description-only-token','planning');
 INSERT INTO tags(name,description) VALUES ('tag-only-token','');`)
	if err == nil {
		_, err = s.Pool.Exec(ctx, `INSERT INTO task_tags(task_id,tag_id) SELECT $1,id FROM tags WHERE name='tag-only-token'`, exact.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body) VALUES (1,'test','agent-only-token',$1,'info','log-only-token')`, exact.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fetch fixtures for the JS oracle only. The production palette query never
	// uses Tasks or hydrates histories, sessions, or more than fifty candidates.
	all, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"needle", "", "tsk1", "description-only-token", "tag-only-token", "closed", "n e", "agent-only-token", "log-only-token"} {
		var want []int64
		paletteNode(t, `const items=input.tasks.map(t=>({id:t.id,label:'TSK-'+t.id+' · '+t.name,search:'TSK-'+t.id+' '+t.name+' '+t.description+' '+t.status+' '+(t.tags||[]).join(' ')})); console.log(JSON.stringify(searchItems(items,input.query).slice(0,50).map(t=>t.id)));`, map[string]any{"tasks": all, "query": q}, &want)
		got, err := s.PaletteTasks(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		ids := []int64{}
		for _, v := range got {
			ids = append(ids, v.ID)
			if len(v.SessionLogs) > 0 {
				t.Fatal("palette hydrated logs")
			}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("query %q SQL=%v JS=%v", q, ids, want)
		}
		if q == "needle" && (len(got) != 50 || got[0].ID != exact.ID) {
			t.Fatalf("exact/top50 lost: %v", ids)
		}
	}
	page, err := s.ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range page.Tasks {
		if v.ID == exact.ID {
			t.Fatal("column activity order changed")
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
	bounded := &Store{Pool: pool}
	if _, err = bounded.PaletteTasks(ctx, "needle"); err != nil {
		t.Fatal(err)
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if len(trace.queries) != 1 {
		t.Fatalf("palette ran %d queries", len(trace.queries))
	}
	query := trace.queries[0].SQL
	if !strings.Contains(query, "LIMIT 50") || strings.Contains(query, "session_logs") || strings.Contains(query, "FROM events") {
		t.Fatalf("unbounded palette hydration: %s", query)
	}
}
