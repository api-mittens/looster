package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
	"github.com/api-mittens/looster/internal/task"
)

func TestHumanCommentMentions(t *testing.T) {
	ctx, s := manifestTestStore(t)
	if _, err := s.ApplyManifest(ctx, `AGENT coder WITH "Code"; AGENT coder-extra WITH "Other"; AGENT Reviewer_2 WITH "Review"; AGENT observer WITH "Observe"; AFTER VALIDATED STOP PINGING coder;`); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "mentions", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(ctx, created.ID, "coder", "existing-coder-session"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(ctx, created.ID, "observer", "existing-observer-session"); err != nil {
		t.Fatal(err)
	}
	check := func(eid int64, want map[string]bool) {
		t.Helper()
		rows, err := s.Pool.Query(ctx, `SELECT agent_name,notification,prompt FROM jobs WHERE event_id=$1 ORDER BY agent_name`, eid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]bool{}
		for rows.Next() {
			var name, prompt string
			var notification bool
			if err := rows.Scan(&name, &notification, &prompt); err != nil {
				t.Fatal(err)
			}
			if _, exists := got[name]; exists {
				t.Fatalf("duplicate dispatch for %s", name)
			}
			got[name] = notification
			if !strings.Contains(prompt, "TSK-") {
				t.Fatalf("missing task context: %q", prompt)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("jobs=%v want %v", got, want)
		}
		for name, notification := range want {
			if actual, ok := got[name]; !ok || actual != notification {
				t.Fatalf("jobs=%v want %v", got, want)
			}
		}
	}
	body := "Please @coder, @coder @Reviewer_2. Ignore @unknown @reviewer_2 @coder-suffix user@coder and @coder_extra."
	eid, err := s.Comment(ctx, created.ID, body)
	if err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"coder": false, "Reviewer_2": false, "observer": true})
	var sid string
	if err := s.Pool.QueryRow(ctx, `SELECT session_id FROM sessions WHERE task_id=$1 AND agent_name='coder'`, created.ID).Scan(&sid); err != nil || sid != "existing-coder-session" {
		t.Fatalf("session changed: %q %v", sid, err)
	}
	// Every separate comment is a deliberate ping, even for existing sessions.
	eid, err = s.Comment(ctx, created.ID, "@coder-extra @coder-extra")
	if err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"coder": true, "coder-extra": false, "observer": true})
	// Agent-authored comments still notify other existing sessions, not mentions.
	eid, err = s.Comment(event.WithActor(ctx, "coder"), created.ID, "@Reviewer_2 @coder-extra")
	if err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"observer": true})
	// Direct SQL follows the same dispatch path.
	if err := s.Pool.QueryRow(ctx, `INSERT INTO events(task_id,kind,body) VALUES($1,'comment','@Reviewer_2') RETURNING id`, created.ID).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"coder": true, "Reviewer_2": false, "observer": true})
	if err := s.SetStatus(ctx, created.ID, task.Validated); err != nil {
		t.Fatal(err)
	}
	eid, err = s.Comment(ctx, created.ID, "@coder @Reviewer_2")
	if err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"Reviewer_2": false, "observer": true})
	if err := s.SetStatus(ctx, created.ID, task.Closed); err != nil {
		t.Fatal(err)
	}
	eid, err = s.Comment(ctx, created.ID, "@Reviewer_2 @coder-extra")
	if err != nil {
		t.Fatal(err)
	}
	check(eid, map[string]bool{"observer": true})
	if _, err := s.Comment(ctx, created.ID, " \n"); err == nil {
		t.Fatal("empty comment accepted")
	}
	if _, err := s.Comment(ctx, -1, "@coder"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing task: %v", err)
	}
}
