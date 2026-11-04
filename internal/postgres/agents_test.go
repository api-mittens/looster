package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAgentsTasklessOnlyMetadata(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to exercise durable agent classification")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TEMP TABLE agents(name text,description text);
 CREATE TEMP TABLE jobs(agent_name text,task_id bigint,context_task_id bigint,state text);
 CREATE TEMP TABLE sessions(agent_name text);
 CREATE TEMP TABLE rules(target text,source text,trigger text);
 INSERT INTO agents VALUES
 ('finished','finished standalone'),('fresh','unclassified'),('scheduled','not run yet'),
 ('session','bound session'),('taskjob','bound job'),('notification','deleted-task context'),
 ('workflow','mixed task workflow'),('source','task workflow source');
 INSERT INTO jobs SELECT name,NULL,NULL,'done' FROM agents WHERE name NOT IN ('fresh','scheduled');
 INSERT INTO sessions VALUES('session');
 INSERT INTO jobs VALUES('taskjob',42,NULL,'done'),('notification',NULL,42,'done');
 INSERT INTO rules VALUES('scheduled',NULL,'every'),('workflow',NULL,'todo'),('workflow','source','completed');`)
	store := &Store{Pool: pool}
	check := func() {
		t.Helper()
		agents, err := store.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(agents) != 8 {
			t.Fatalf("agents: %+v", agents)
		}
		for i, a := range agents {
			want := a.Name == "finished" || a.Name == "scheduled"
			if a.TasklessOnly != want {
				t.Errorf("%s taskless_only=%v, want %v", a.Name, a.TasklessOnly, want)
			}
			if a.Description == "" {
				t.Errorf("description lost: %+v", a)
			}
			if i > 0 && agents[i-1].Name >= a.Name {
				t.Error("agent ordering changed")
			}
		}
	}
	check()
	// Neither completion nor retry/failure state changes classification.
	for _, state := range []string{"running", "archiving", "failed", "pending", "done"} {
		exec("UPDATE jobs SET state='" + state + "' WHERE agent_name='finished'")
		check()
	}
}
