// Disposable local integration runner. Never reads application configuration.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/api-mittens/looster/internal/ducklakefixture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (result error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	scratch := os.Getenv("JCODE_SCRATCH_DIR")
	if scratch == "" {
		return fmt.Errorf("set JCODE_SCRATCH_DIR to a scratch directory")
	}
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return err
	}
	root, err := os.MkdirTemp(scratch, "ducklake-")
	if err != nil {
		return err
	}
	removeRoot := true
	defer func() {
		if removeRoot {
			_ = os.RemoveAll(root)
		}
	}()
	// Deliberately allowlist process environment: no PG*, AWS*, LOOSTER* or
	// application config can redirect this runner to a production service.
	env := commandEnvironment(root)
	command := func(ctx context.Context, name string, args ...string) error {
		c := exec.CommandContext(ctx, name, args...)
		c.Env = env
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	bin := os.Getenv("LOOSTER_TEST_PG_BIN")
	if bin == "" {
		bin = "/opt/homebrew/opt/postgresql@17/bin"
	}
	for _, name := range []string{"initdb", "pg_ctl", "createdb"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	data := filepath.Join(root, "pg")
	if err := command(ctx, filepath.Join(bin, "initdb"), "-D", data, "-U", "fixture", "-A", "trust", "--no-locale", "--encoding=UTF8"); err != nil {
		return err
	}
	defer func() {
		if _, err := os.Stat(filepath.Join(data, "postmaster.pid")); os.IsNotExist(err) {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := command(cleanup, filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "immediate", "-w", "stop"); err != nil {
			removeRoot = false
			result = fmt.Errorf("cluster cleanup failed, preserved %s: %w", root, err)
		}
	}()
	// Disable Unix sockets to avoid path-length limits. Listen on loopback only.
	if err := command(ctx, filepath.Join(bin, "pg_ctl"), "-D", data, "-l", filepath.Join(root, "postgres.log"), "-o", "-h 127.0.0.1 -p "+port+" -k '' -F", "-w", "start"); err != nil {
		if log, readErr := os.ReadFile(filepath.Join(root, "postgres.log")); readErr == nil {
			fmt.Fprintln(os.Stderr, string(log))
		}
		return err
	}
	if err := command(ctx, filepath.Join(bin, "createdb"), "-h", "127.0.0.1", "-p", port, "-U", "fixture", "ducklake_fixture"); err != nil {
		return err
	}
	s3, err := ducklakefixture.Start()
	if err != nil {
		return err
	}
	defer s3.Close()
	defer func() { fmt.Println("Disposable S3 operations:", s3.Requests()) }()
	// Resolve only Go's cache paths before isolating HOME. Native extensions and
	// all home-directory configuration for tests must be disposable as well.
	cacheCommand := exec.CommandContext(ctx, "go", "env", "GOCACHE", "GOMODCACHE")
	cacheCommand.Env = env
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		return fmt.Errorf("resolve Go caches: %w", err)
	}
	caches := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(caches) != 2 {
		return fmt.Errorf("unexpected Go cache paths")
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	env = append(env, "HOME="+home, "GOCACHE="+caches[0], "GOMODCACHE="+caches[1])
	env = append(env,
		"LOOSTER_TEST_PROVISION_DATABASE_URL=postgres://fixture@127.0.0.1:"+port+"/postgres?sslmode=disable",
		"LOOSTER_DUCKLAKE_DISPOSABLE_PROBE=yes",
		"LOOSTER_TEST_DUCKLAKE_LIBPQ=host=127.0.0.1 port="+port+" user=fixture dbname=ducklake_fixture sslmode=disable",
		"LOOSTER_TEST_DUCKLAKE_DATA_PATH=s3://fixture/disposable/",
		"LOOSTER_TEST_S3_ENDPOINT="+s3.Endpoint(),
		"LOOSTER_TEST_S3_ACCESS_KEY=fixture-access", "LOOSTER_TEST_S3_SECRET_KEY=fixture-secret", "LOOSTER_TEST_S3_REGION=us-east-1")
	for _, test := range []struct{ pkg, pattern string }{{"./internal/postgres", "^TestProvisionIntegration"}, {"./internal/ducklake", "^TestDisposableIntegration$"}} {
		fmt.Println("Disposable integration:", test.pkg, strings.Trim(test.pattern, "^$"))
		if err := command(ctx, "go", "test", "-count=1", "-v", "-timeout=5m", test.pkg, "-run", test.pattern); err != nil {
			return err
		}
	}
	return nil
}

func commandEnvironment(root string) []string {
	env := []string{}
	for _, key := range []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTOOLCHAIN", "SDKROOT", "DEVELOPER_DIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "TMPDIR="+root, "LC_ALL=C", "LANG=C")
}
