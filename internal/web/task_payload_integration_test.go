package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/ducklake"
	"github.com/api-mittens/looster/internal/ducklakefixture"
	"github.com/api-mittens/looster/internal/objectstore"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5"
)

// This reader records calls and sends reads to the native payload store.
type taskHTTPPayloadReader struct {
	native  *ducklake.PayloadStore
	mu      sync.Mutex
	batches [][]string
	gets    int
	fail    bool
	started chan struct{}
	release chan struct{}
}

func (r *taskHTTPPayloadReader) Put(ctx context.Context, key string, value []byte) error {
	return r.native.Put(ctx, key, value)
}

func (r *taskHTTPPayloadReader) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	r.mu.Lock()
	r.gets++
	r.mu.Unlock()
	return r.native.GetMany(ctx, keys)
}

func (r *taskHTTPPayloadReader) Get(ctx context.Context, key string) ([]byte, error) {
	r.mu.Lock()
	r.gets++
	r.mu.Unlock()
	return r.native.Get(ctx, key)
}

func (r *taskHTTPPayloadReader) GetManyBounded(ctx context.Context, keys []string, limit int64) (map[string][]byte, error) {
	r.mu.Lock()
	r.batches = append(r.batches, append([]string(nil), keys...))
	fail, started, release := r.fail, r.started, r.release
	r.mu.Unlock()
	if fail {
		return nil, errors.New("Test payload read failed")
	}
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if limit <= 0 || limit > postgres.TaskPayloadMaxBytes {
		return nil, errors.New("The payload byte limit is not safe")
	}
	return r.native.GetManyBounded(ctx, keys, limit)
}

func taskHTTPPayloadKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// This test creates and drops only its own database. It requires a local test URL.
func TestTaskPayloadHTTPIntegration(t *testing.T) {
	raw := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("Set LOOSTER_TEST_DATABASE_URL to a local test cluster")
	}
	// Do not inherit a host, credential file, or session option from the shell.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "PG") {
			t.Setenv(name, "")
		}
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal("Invalid test database URL")
	}
	if cfg.Host != "127.0.0.1" && cfg.Host != "localhost" && cfg.Host != "::1" {
		t.Fatal("The test database must use a loopback host")
	}
	for _, fallback := range cfg.Fallbacks {
		if fallback.Host != "127.0.0.1" && fallback.Host != "localhost" && fallback.Host != "::1" {
			t.Fatal("Each test database host must use loopback")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("tsk80_http_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("Test database cleanup failed: %v", err)
		}
	}()
	// Select only the new database. Do not use a managed namespace override.
	connectionURL, err := url.Parse(raw)
	if err != nil || (connectionURL.Scheme != "postgres" && connectionURL.Scheme != "postgresql") {
		t.Fatal("The test requires a PostgreSQL URL")
	}
	connectionURL.Path = "/" + name
	connectionURL.RawPath = ""
	query := connectionURL.Query()
	query.Del("dbname")
	query.Del("database")
	connectionURL.RawQuery = query.Encode()
	selected := connectionURL.String()
	if err := postgres.EnsureNamespace(ctx, selected, name); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.OpenNamespace(ctx, selected, name)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s3, err := ducklakefixture.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	catalog, err := postgres.DatabaseConnectionString(selected, name)
	if err != nil {
		t.Fatal(err)
	}
	lakeConfig := ducklake.Config{
		CatalogConnection: catalog, Namespace: name, DataPath: "s3://test/" + name + "/",
		S3: objectstore.Config{Endpoint: s3.Endpoint(), Region: "us-east-1", Bucket: "test", AccessKey: "test", SecretKey: "test"},
	}
	lake, err := ducklake.Open(ctx, lakeConfig)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := ducklake.NewPayloadStore(ctx, lake, ducklake.PayloadOptions{SpoolDir: t.TempDir(), FlushInterval: time.Millisecond})
	if err != nil {
		lake.Close()
		t.Fatal(err)
	}
	defer func() {
		if err := payloads.Close(); err != nil {
			t.Errorf("Payload store close failed: %v", err)
		}
	}()
	text := "The real task payload is available."
	key := taskHTTPPayloadKey(text)
	unrelatedText := "This payload belongs to another task."
	unrelatedKey := taskHTTPPayloadKey(unrelatedText)
	for k, v := range map[string]string{key: text, unrelatedKey: unrelatedText} {
		if err := payloads.Put(ctx, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
		got, err := payloads.GetCommitted(ctx, k)
		if err != nil || string(got) != v {
			t.Fatalf("Committed payload check failed: %v", err)
		}
	}
	item, err := store.CreateTask(ctx, "HTTP test", "Task text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateTask(ctx, "Other task", "Other text", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAgent(ctx, "reader", "Read task data"); err != nil {
		t.Fatal(err)
	}
	ref := "looster-payload:v1:" + key
	otherRef := "looster-payload:v1:" + unrelatedKey
	for _, fixture := range []struct {
		id  int64
		ref string
		job int64
	}{{item.ID, ref, 100}, {other.ID, otherRef, 1000}} {
		count := 3
		if fixture.id == item.ID {
			count = 205
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name,session_id) VALUES($1,'reader',$2)`, fixture.id, fmt.Sprint(fixture.job)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO agent_session_runs(job_id,task_id,agent_name,session_id,started_at,outcome,summary,summary_status,error) SELECT $3+i,$1,'reader',$3::text,now(),'failed',$2,'available',$2 FROM generate_series(0,$4::int-1) i`, fixture.id, fixture.ref, fixture.job, count); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,task_id,agent_name,session_id,kind,body) SELECT $3+i,$1,'reader',$3::text,'note',$2 FROM generate_series(0,$4::int-1) i`, fixture.id, fixture.ref, fixture.job, count); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Comment(ctx, item.ID, "A test comment"); err != nil {
		t.Fatal(err)
	}
	reader := &taskHTTPPayloadReader{native: payloads}
	store.SetPayloadStore(reader)
	secured, err := app.WithPermissions(store, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(secured))
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	base := fmt.Sprintf("/api/tasks/TSK-%d", item.ID)
	get := func(path string, status int) []byte {
		t.Helper()
		start := time.Now()
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("HTTP %s status=%d time=%s bytes=%d", path, response.StatusCode, time.Since(start), len(body))
		if response.StatusCode != status {
			t.Fatalf("HTTP %s: status %d, body %s", path, response.StatusCode, body)
		}
		if strings.Contains(string(body), "looster-payload:v1:") {
			t.Fatalf("HTTP %s contains a payload reference", path)
		}
		return body
	}
	metadata := func() {
		t.Helper()
		reader.mu.Lock()
		before, gets := len(reader.batches), reader.gets
		reader.mu.Unlock()
		for _, path := range []string{"/api/board/list", "/api/board", base, "/api/status", base + "/events?kind=comment"} {
			body := get(path, 200)
			if strings.Contains(string(body), text) || strings.Contains(string(body), unrelatedText) {
				t.Fatalf("Metadata contains payload text: %s", path)
			}
			if strings.Contains(path, "events?") && !strings.Contains(string(body), "A test comment") {
				t.Fatal("The comment is missing")
			}
		}
		reader.mu.Lock()
		defer reader.mu.Unlock()
		if len(reader.batches) != before || reader.gets != gets {
			t.Fatal("Metadata read payload data")
		}
	}
	reader.mu.Lock()
	reader.fail = true
	reader.mu.Unlock()
	metadata()
	get(base+"/payloads", 500)
	metadata()
	reader.mu.Lock()
	reader.fail = false
	reader.batches = nil
	reader.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		body := get(base+"/payloads", 200)
		var page struct {
			Task     task.Task          `json:"task"`
			Sessions []agent.Session    `json:"sessions"`
			Runs     []agent.SessionRun `json:"runs"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != 50 || len(page.Task.SessionLogs) != 200 {
			t.Fatalf("Unexpected history size: runs=%d logs=%d", len(page.Runs), len(page.Task.SessionLogs))
		}
		if len(page.Sessions) != 1 || page.Sessions[0].Summary != text || page.Sessions[0].Error != text {
			t.Fatal("The session payload text is not correct")
		}
		t.Logf("Full payload read %d: runs=%d logs=%d sessions=%d", attempt+1, len(page.Runs), len(page.Task.SessionLogs), len(page.Sessions))
		for _, run := range page.Runs {
			if run.Summary != text || run.Error != text {
				t.Fatal("The run payload text is not correct")
			}
		}
		for _, log := range page.Task.SessionLogs {
			if log.Body != text {
				t.Fatal("The log payload text is not correct")
			}
		}
		reader.mu.Lock()
		if len(reader.batches) != attempt+1 || len(reader.batches[attempt]) != 1 || reader.batches[attempt][0] != key || reader.gets != 0 {
			t.Fatalf("Unexpected batch reads: %v, single reads=%d", reader.batches, reader.gets)
		}
		reader.mu.Unlock()
	}
	// Hold one payload read while metadata requests use the real HTTP server.
	reader.mu.Lock()
	reader.started = make(chan struct{}, 1)
	reader.release = make(chan struct{})
	started, release := reader.started, reader.release
	reader.mu.Unlock()
	delayed := make(chan error, 1)
	go func() {
		response, err := client.Get(server.URL + base + "/payloads")
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				err = fmt.Errorf("Unexpected delayed status: %d", response.StatusCode)
			}
		}
		delayed <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		close(release)
		t.Fatal(ctx.Err())
	}
	func() { defer close(release); metadata() }()
	if err := <-delayed; err != nil {
		t.Fatal(err)
	}
	reader.mu.Lock()
	reader.started = nil
	reader.release = nil
	reader.mu.Unlock()
	// Read bounded history while a real payload write commits.
	writeDone := make(chan error, 1)
	go func() {
		value := "A concurrent payload write"
		writeDone <- payloads.Put(ctx, taskHTTPPayloadKey(value), []byte(value))
	}()
	for i := 0; i < 3; i++ {
		get(base+"/payloads?limit=1&log_limit=1", 200)
		metadata()
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	body := get(base+"/payloads?limit=1&log_limit=1", 200)
	var bounded struct {
		Runs    []agent.SessionRun `json:"runs"`
		Task    task.Task          `json:"task"`
		Next    int64              `json:"next_before_job_id"`
		NextLog int64              `json:"next_before_log_id"`
	}
	if err := json.Unmarshal(body, &bounded); err != nil {
		t.Fatal(err)
	}
	if len(bounded.Runs) != 1 || len(bounded.Task.SessionLogs) != 1 || bounded.Next == 0 || bounded.NextLog == 0 {
		t.Fatalf("Unexpected bounded page: %+v", bounded)
	}
	nextBody := get(fmt.Sprintf("%s/payloads?limit=1&log_limit=1&before_job_id=%d&before_log_id=%d", base, bounded.Next, bounded.NextLog), 200)
	var next struct {
		Runs []agent.SessionRun `json:"runs"`
		Task task.Task          `json:"task"`
	}
	if err := json.Unmarshal(nextBody, &next); err != nil {
		t.Fatal(err)
	}
	if len(next.Runs) != 1 || next.Runs[0].JobID >= bounded.Next || len(next.Task.SessionLogs) != 1 || next.Task.SessionLogs[0].ID >= bounded.NextLog {
		t.Fatal("The page cursors did not exclude prior records")
	}
	reader.mu.Lock()
	if reader.gets != 0 {
		t.Fatal("A payload read did not use the bounded batch method")
	}
	for _, keys := range reader.batches {
		if len(keys) != 1 || keys[0] != key {
			t.Fatalf("A read used an unrelated key: %v", keys)
		}
	}
	reader.mu.Unlock()
	// A separate process must read the committed catalog and S3 objects.
	encoded, err := json.Marshal(lakeConfig)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-test.run=^TestTaskPayloadNativeReaderProcess$", "-test.v")
	command.Env = append(os.Environ(), "LOOSTER_HTTP_READER_CONFIG="+string(encoded), "LOOSTER_HTTP_READER_KEY="+key, "LOOSTER_HTTP_READER_TEXT="+text)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("The native reader process failed: %v\n%s", err, output)
	}
	t.Logf("Native reader process: %s", output)
	operations := s3.Requests()
	if operations["PUT"] < 3 || operations["GET"] == 0 {
		t.Fatalf("The S3 fixture did not serve real payload data: %v", operations)
	}
	t.Logf("Pinned engine=%s DuckLake=%s build=%s postgres=%s httpfs=%s S3 operations=%v", ducklake.EngineVersion, ducklake.DuckLakeVersion, ducklake.DuckLakeExtensionVersion, ducklake.PostgresExtensionVersion, ducklake.HTTPFSExtensionVersion, operations)
}

func TestTaskPayloadNativeReaderProcess(t *testing.T) {
	encoded := os.Getenv("LOOSTER_HTTP_READER_CONFIG")
	if encoded == "" {
		t.Skip("The parent test starts this reader process")
	}
	var config ducklake.Config
	if err := json.Unmarshal([]byte(encoded), &config); err != nil {
		t.Fatal("Invalid reader configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lake, err := ducklake.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := ducklake.NewPayloadStore(ctx, lake, ducklake.PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		lake.Close()
		t.Fatal(err)
	}
	defer payloads.Close()
	key := os.Getenv("LOOSTER_HTTP_READER_KEY")
	rows, err := payloads.GetManyBounded(ctx, []string{key}, postgres.TaskPayloadMaxBytes)
	if err != nil || string(rows[key]) != os.Getenv("LOOSTER_HTTP_READER_TEXT") {
		t.Fatalf("The native catalog read failed: %v", err)
	}
	t.Log("The separate native process read the committed payload")
}
