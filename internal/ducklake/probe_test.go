package ducklake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recordedSession struct {
	statements []string
	closed     bool
	fail       bool
}

func (s *recordedSession) Exec(_ context.Context, q string) error {
	if s.closed {
		return ErrClosed
	}
	s.statements = append(s.statements, q)
	if s.fail {
		return errors.New("test failure")
	}
	return nil
}
func (s *recordedSession) Close() error { s.closed = true; return nil }

func TestProbeFreshReopen(t *testing.T) {
	for _, failure := range []string{"", "write", "reopen", "read"} {
		t.Run(failure, func(t *testing.T) {
			writer := &recordedSession{fail: failure == "write"}
			reader := &recordedSession{fail: failure == "read"}
			calls := 0
			err := probeDisposable(context.Background(), config(), func(context.Context, Config) (probeSession, error) {
				calls++
				if calls == 1 {
					return writer, nil
				}
				if calls != 2 || !writer.closed {
					t.Fatal("reader opened before writer engine closed")
				}
				if failure == "reopen" {
					return nil, errors.New("reopen failed")
				}
				return reader, nil
			})
			if (err != nil) != (failure != "") {
				t.Fatalf("unexpected result %v", err)
			}
			if !writer.closed {
				t.Fatal("writer not closed")
			}
			if failure == "write" {
				if calls != 1 {
					t.Fatal("continued after write failure")
				}
				return
			}
			if calls != 2 {
				t.Fatal("probe did not open fresh reader")
			}
			if failure == "reopen" {
				return
			}
			if !reader.closed {
				t.Fatal("reader not closed")
			}
			if len(writer.statements) != 1 || !strings.Contains(writer.statements[0], "INSERT INTO lake.main.looster_probe_") {
				t.Fatal("missing parquet-backed write")
			}
			if !strings.Contains(reader.statements[0], "array_agg(id)=[42]") {
				t.Fatal("missing real row read")
			}
			if failure == "read" {
				if len(reader.statements) != 1 {
					t.Fatal("cleanup continued after failed read")
				}
				return
			}
			if len(reader.statements) != 2 || !strings.Contains(reader.statements[1], "ducklake_cleanup_old_files('lake', cleanup_all => true)") {
				t.Fatal("missing cleanup")
			}
		})
	}
}

func TestOpenContextDoesNotOwnLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l, err := openEngine(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer l.Close()
	cancel()
	if err := l.Exec(context.Background(), "SELECT 42"); err != nil {
		t.Fatal("initialization context shortened lake lifetime", err)
	}
}

func TestQueuedCancellation(t *testing.T) {
	l := engine(t)
	// Holding the execution gate models another active session operation.
	l.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := l.Exec(ctx, "SELECT 42")
	<-l.gate
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation lost: %v", err)
	}
}

func TestAmbientLibpqRejected(t *testing.T) {
	t.Setenv("PGPASSWORD", "ambient-secret-canary")
	if err := Validate(config()); err != nil {
		t.Fatal("offline validation changed", err)
	}
	_, err := Open(context.Background(), config())
	if err == nil || !strings.Contains(err.Error(), "ambient PG*") || strings.Contains(err.Error(), "canary") {
		t.Fatal("ambient libpq credentials not safely rejected")
	}
}
