package jcode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func TestCancellationTakesPrecedenceOverStreamError(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "jcode")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"start\",\"session_id\":\"interrupted\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := Harness{Binary: binary, SessionsDir: dir}
	_, err := h.Run(ctx, app.Run{Agent: "coder", Prompt: "continue"}, func(app.Output) error {
		cancel()
		return errors.New("connection closed during shutdown")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected resumable cancellation, got %v", err)
	}
}

func TestHarnessProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		history      bool
		bad          bool
	}{
		{"complete", "{\"type\":\"start\",\"session_id\":\"session_test\"}\n{\"type\":\"done\",\"session_id\":\"session_test\"}\n", true, false},
		{"restore", "{\"type\":\"start\",\"session_id\":\"session_test\"}\n{\"type\":\"done\",\"session_id\":\"session_test\"}\n", false, false},
		{"incomplete", "{\"type\":\"start\",\"session_id\":\"session_test\"}\n", true, true},
		{"history missing", "{\"type\":\"done\",\"session_id\":\"session_test\"}\n", false, true},
		{"bad session", "{\"type\":\"done\",\"session_id\":\"../secret\"}\n", true, true},
		{"error event", "{\"type\":\"error\",\"message\":\"failed\"}\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "jcode")
			script := "#!/bin/sh\n[ \"$LOOSTER_DATABASE_URL\" = 'postgres://shared' ] || exit 9\n[ -z \"$LOOSTER_AGENT_DATABASE_URLS_OTHER\" ] || exit 10\n[ \"$PGPASSWORD\" = 'shared-password' ] || exit 11\n[ \"$LOOSTER_ROLE\" = 'agent' ] && [ \"$LOOSTER_AGENT\" = 'coder' ] || exit 12\nprintf '%s' '" + tc.stream + "'\n"
			if e := os.WriteFile(binary, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			if tc.history {
				if e := os.WriteFile(filepath.Join(dir, "session_test.json"), []byte(`{"messages":[{"content":"full context"}]}`), 0600); e != nil {
					t.Fatal(e)
				}
			}
			t.Setenv("LOOSTER_DATABASE_URL", "postgres://worker")
			t.Setenv("LOOSTER_AGENT_DATABASE_URLS_OTHER", "postgres://other-agent")
			t.Setenv("PGPASSWORD", "shared-password")
			t.Setenv("LOOSTER_ROLE", "human")
			t.Setenv("LOOSTER_AGENT", "someone-else")
			h := Harness{Binary: binary, SessionsDir: dir, DatabaseURL: "postgres://shared"}
			if tc.name == "restore" {
				h.Objects = archiveStore{t: t}
			}
			var captured string
			r, e := h.Run(context.Background(), app.Run{Agent: "coder", Task: task.Task{ID: 1}, Workdir: filepath.Join(dir, "work"), SessionID: "session_test", Prompt: "test"}, func(o app.Output) error { captured += o.Text; return nil })
			if (e != nil) != tc.bad {
				t.Fatalf("error %v", e)
			}
			if !tc.bad && (!strings.Contains(string(r.History), "full context") || !strings.Contains(captured, "start")) {
				t.Fatal("missing full history or events")
			}
		})
	}
}

type archiveStore struct{ t *testing.T }

func (s archiveStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if key != "sessions/coder/TSK-1/session_test.json" {
		s.t.Fatalf("wrong archive key %s", key)
	}
	return io.NopCloser(strings.NewReader(`{"messages":[{"content":"full context"}]}`)), nil
}
func (archiveStore) Put(context.Context, string, io.Reader, int64) error { panic("unexpected upload") }

func TestStandaloneScheduledInvocation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "jcode")
	script := `#!/bin/sh
[ -z "${LOOSTER_TASK_ID+x}" ] || exit 10
[ "$LOOSTER_AGENT" = coder ] && [ "$LOOSTER_ROLE" = agent ] || exit 11
for arg do
 case "$arg" in -C|--resume) exit 12 ;; esac
done
printf '%s\n' '{"type":"done","session_id":"scheduled_test"}'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scheduled_test.json"), []byte(`{"messages":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOSTER_TASK_ID", "TSK-999")
	h := Harness{Binary: binary, SessionsDir: dir}
	r, err := h.Run(context.Background(), app.Run{Agent: "coder", Prompt: "Triage inbound requests.", ArchivePrefix: "sessions/coder/scheduled/job-1"}, func(app.Output) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.SessionID != "scheduled_test" {
		t.Fatal("scheduled run did not capture its new session ID")
	}
}
