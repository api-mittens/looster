package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// exerciseCLI uses only this runner's newly created cluster. No application
// configuration is read, and the compiled CLI has no executable search path.
func exerciseCLI(ctx context.Context, root, port, endpoint string, env []string) error {
	binary := filepath.Join(root, "looster")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/looster")
	build.Env = env
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("compile integration CLI: %w", err)
	}
	base := "postgres://fixture@127.0.0.1:" + port + "/"
	admin, err := pgx.Connect(ctx, base+"postgres?sslmode=disable")
	if err != nil {
		return err
	}
	defer admin.Close(context.Background())
	const database = "fixture_custom_existing_app"
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		return err
	}
	app, err := pgx.Connect(ctx, base+database+"?sslmode=disable")
	if err != nil {
		return err
	}
	defer app.Close(context.Background())
	var originalOID uint32
	if err := admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", database).Scan(&originalOID); err != nil {
		return err
	}
	if _, err := app.Exec(ctx, "CREATE TABLE public.fixture_existing_sentinel(value text PRIMARY KEY); INSERT INTO public.fixture_existing_sentinel VALUES ('existing-custom-app')"); err != nil {
		return err
	}
	configuration := filepath.Join(root, "cli-config.toml")
	if err := os.WriteFile(configuration, []byte(fmt.Sprintf("namespace = \"default\"\n[s3]\nendpoint = %q\nregion = \"us-east-1\"\nbucket = \"fixture\"\n", endpoint)), 0600); err != nil {
		return err
	}
	cliEnv := []string{}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "PATH" && !strings.HasPrefix(key, "LOOSTER") && !strings.HasPrefix(key, "AWS") && !strings.HasPrefix(key, "PG") {
			cliEnv = append(cliEnv, entry)
		}
	}
	cliEnv = append(cliEnv, "PATH=", "LOOSTER_DATABASE_URL="+base+database+"?sslmode=disable", "LOOSTER_S3_ACCESS_KEY=fixture-access", "LOOSTER_S3_SECRET_KEY=fixture-secret", "LOOSTER_ROLE=human")
	run := func(namespace string, ducklake bool) error {
		args := []string{"--config", configuration, "--namespace", namespace, "configure", "storage"}
		if ducklake {
			args = append(args, "--ducklake")
		}
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = cliEnv
		command.Dir = root
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("compiled CLI namespace %s ducklake=%t: %w", namespace, ducklake, err)
		}
		return nil
	}
	schemas := []string{"public", "looster_ns_dom", "looster_ns_nume"}
	versions := map[string]int{}
	for i, namespace := range []string{"default", "dom", "nume"} {
		if err := run(namespace, false); err != nil {
			return err
		}
		schema := pgx.Identifier{schemas[i]}.Sanitize()
		if _, err := app.Exec(ctx, "INSERT INTO "+schema+".tasks(name,description) VALUES ($1,'disposable CLI sentinel')", "sentinel-"+namespace); err != nil {
			return err
		}
		var count int
		if err := app.QueryRow(ctx, "SELECT count(*) FROM "+schema+".goose_db_version").Scan(&count); err != nil {
			return err
		}
		versions[schemas[i]] = count
	}
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_database WHERE datname='ducklake')").Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("default catalog existed before CLI opt-in")
	}
	for repeat := 0; repeat < 2; repeat++ {
		if err := run("default", true); err != nil {
			return err
		}
	}
	var currentOID uint32
	if err := admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", database).Scan(&currentOID); err != nil {
		return err
	}
	if currentOID != originalOID {
		return fmt.Errorf("existing custom application database replaced")
	}
	if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_database WHERE datname='ducklake') AND NOT EXISTS(SELECT FROM pg_database WHERE datname='looster')").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("default catalog missing or unintended looster application database created")
	}
	var retained bool
	if err := app.QueryRow(ctx, "SELECT EXISTS(SELECT FROM public.fixture_existing_sentinel WHERE value='existing-custom-app')").Scan(&retained); err != nil {
		return err
	}
	if !retained {
		return fmt.Errorf("existing application sentinel lost")
	}
	for i, namespace := range []string{"default", "dom", "nume"} {
		schema := pgx.Identifier{schemas[i]}.Sanitize()
		if err := app.QueryRow(ctx, "SELECT EXISTS(SELECT FROM "+schema+".tasks WHERE name=$1 AND description='disposable CLI sentinel')", "sentinel-"+namespace).Scan(&retained); err != nil {
			return err
		}
		if !retained {
			return fmt.Errorf("namespace %s sentinel lost", namespace)
		}
		var count int
		if err := app.QueryRow(ctx, "SELECT count(*) FROM "+schema+".goose_db_version").Scan(&count); err != nil {
			return err
		}
		if count != versions[schemas[i]] {
			return fmt.Errorf("namespace %s migration history changed on repeat", namespace)
		}
	}
	catalog, err := pgx.Connect(ctx, base+"ducklake?sslmode=disable")
	if err != nil {
		return err
	}
	defer catalog.Close(context.Background())
	if err := catalog.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'ducklake_%') AND to_regclass('public.tasks') IS NULL AND to_regclass('public.goose_db_version') IS NULL").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("default catalog metadata missing or application migrations leaked into catalog")
	}
	if err := catalog.QueryRow(ctx, "SELECT EXISTS(SELECT FROM public.ducklake_metadata WHERE key='data_path' AND value='s3://fixture/ducklake/')").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("default catalog did not retain the configured bucket /ducklake/ prefix")
	}
	fmt.Println("Compiled CLI PASS: empty PATH, private HOME/config, default catalog created, custom application database and default/dom/nume sentinels retained after repeated initialization")
	return nil
}
