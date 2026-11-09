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

func TestAmbientLibpqOverrides(t *testing.T) {
	for environment := range libpqEnvironment {
		t.Setenv(environment, "")
	}
	for _, environment := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGSSLROOTCERT"} {
		t.Setenv(environment, "ambient-secret-canary")
	}
	c := config()
	if err := Validate(c); err != nil {
		t.Fatal("explicit resolved settings must override libpq environment", err)
	}
	p, err := prepare(c)
	if err != nil || strings.Contains(p, "ambient-secret-canary") {
		t.Fatal("ambient defaults were substituted into resolved settings")
	}
	t.Setenv("PGAPPNAME", "ambient-app-canary")
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "PGAPPNAME") || strings.Contains(err.Error(), "canary") {
		t.Fatal("offline validation accepted unrepresented ambient default")
	}
	if _, err := Open(context.Background(), c); err == nil || !strings.Contains(err.Error(), "PGAPPNAME") {
		t.Fatal("unsafe ambient default reached engine")
	}
	c.CatalogConnection += " application_name='resolved-app'"
	if err := Validate(c); err != nil {
		t.Fatal("explicit application name did not override environment", err)
	}
	c.CatalogConnection += " application_name=''"
	if err := Validate(c); err == nil {
		t.Fatal("empty final override accepted ambient default")
	}
	t.Setenv("PGAPPNAME", "")
	t.Setenv("PGSERVICE", "ambient-service-canary")
	if err := Validate(config()); err == nil || !strings.Contains(err.Error(), "PGSERVICE") {
		t.Fatal("ambient service accepted")
	}
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGPASSFILE", "/ambient-secret-canary")
	t.Setenv("PGSERVICEFILE", "/ambient-service-canary")
	t.Setenv("PG_APPLICATION_CUSTOM", "not-a-libpq-default")
	if err := Validate(config()); err != nil {
		t.Fatal("disabled password file or inactive service file rejected", err)
	}
}
