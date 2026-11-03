// Package cli contains the CLI application and its configuration boundary.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/jcode"
	"github.com/dominikbb/looster/internal/namespace"
	"github.com/dominikbb/looster/internal/objectstore"
	"github.com/dominikbb/looster/internal/postgres"
	"github.com/dominikbb/looster/internal/task"
)

// Version is set to the release tag by the release build.
var Version = "dev"

type application struct {
	v      *viper.Viper
	repo   app.Repository
	store  *postgres.Store
	config string
}

func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	a := &application{v: viper.New()}
	root := a.command()
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	defer func() {
		if a.store != nil {
			a.store.Close()
		}
	}()
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil && cmd != nil && cmd.Name() == "serve" && a.store != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if logErr := a.store.RecordSystemEvent(cleanup, "worker_start_failed", err.Error()); logErr != nil {
			fmt.Fprintln(errOut, "looster: could not persist worker error:", logErr)
		}
	}
	return err
}
func (a *application) command() *cobra.Command {
	v := a.v
	v.SetEnvPrefix("LOOSTER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	defaults := map[string]any{"role": "human", "harness": "jcode", "workdir": "./workdirs", "concurrency": 4, "poll_interval": "1s", "run_timeout": "1h", "s3.region": "us-east-1", "jcode.binary": "jcode"}
	for k, x := range defaults {
		v.SetDefault(k, x)
	}
	root := &cobra.Command{Use: "looster", Version: Version, Short: "Orchestrate agent tasks across machines", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&a.config, "config", "", "TOML configuration file (default ~/.config/looster/config.toml)")
	root.PersistentFlags().String("database-url", "", "PostgreSQL connection string (LOOSTER_DATABASE_URL); defaults to PG* environment variables")
	_ = v.BindPFlag("database_url", root.PersistentFlags().Lookup("database-url"))
	root.PersistentFlags().StringP("namespace", "N", namespace.Default, "Database namespace (LOOSTER_NAMESPACE)")
	_ = v.BindPFlag("namespace", root.PersistentFlags().Lookup("namespace"))
	root.PersistentFlags().String("role", "human", "Application role: human or agent (LOOSTER_ROLE)")
	root.PersistentFlags().String("agent-name", "", "Agent identity for agent mode (LOOSTER_AGENT)")
	_ = v.BindPFlag("role", root.PersistentFlags().Lookup("role"))
	_ = v.BindPFlag("agent", root.PersistentFlags().Lookup("agent-name"))
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Annotations["offline"] == "true" {
			return nil
		}
		if e := a.load(); e != nil {
			return e
		}
		if err := namespace.Validate(v.GetString("namespace")); err != nil {
			return err
		}
		actor, err := app.NewActor(v.GetString("role"), v.GetString("agent"))
		if err != nil {
			return err
		}
		if cmd.Annotations["human"] == "true" {
			if err = actor.RequireHuman(); err != nil {
				return err
			}
		}
		if cmd.Annotations["local"] == "true" {
			return nil
		}
		s, e := postgres.OpenNamespace(cmd.Context(), v.GetString("database_url"), v.GetString("namespace"))
		if e != nil {
			return e
		}
		a.store = s
		if actor.Role == app.Agent {
			if _, err = s.Agent(cmd.Context(), actor.Name); err != nil {
				return fmt.Errorf("agent %q: %w", actor.Name, err)
			}
		}
		a.repo, err = app.WithPermissions(s, actor)
		return err
	}
	root.AddCommand(a.taskCommand(), a.ownerCommand("tag"), a.ownerCommand("agent"), a.behaviourCommand(), a.serveCommand(), a.webCommand(), a.namespaceCommand(), skillsCommand(), a.logsCommand())
	status := &cobra.Command{Use: "status", Short: "List live servers (30 second heartbeat window)", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		w, e := a.repo.Workers(c.Context())
		if e != nil {
			return e
		}
		if e = output(c, w); e != nil {
			return e
		}
		if len(w) == 0 {
			return errors.New("no live looster servers")
		}
		return nil
	}}
	root.AddCommand(status)
	db := &cobra.Command{Use: "db", Short: "Database maintenance"}
	db.AddCommand(&cobra.Command{Use: "migrate", Short: "Apply embedded Goose migrations (requires schema owner)", Args: cobra.NoArgs, Annotations: map[string]string{"local": "true", "human": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		url := v.GetString("database_url")
		if e := postgres.MigrateNamespace(c.Context(), url, v.GetString("namespace")); e != nil {
			return e
		}
		fmt.Fprintln(c.OutOrStdout(), "Database migrated")
		return nil
	}})
	root.AddCommand(db)
	return root
}
func (a *application) load() error {
	if a.config != "" {
		a.v.SetConfigFile(a.config)
	} else {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		a.v.SetConfigFile(filepath.Join(base, "looster", "config.toml"))
	}
	e := a.v.ReadInConfig()
	if e != nil {
		var missing viper.ConfigFileNotFoundError
		if a.config == "" && (os.IsNotExist(e) || errors.As(e, &missing)) {
			return nil
		}
		return e
	}
	return nil
}
func output(c *cobra.Command, v any) error {
	enc := json.NewEncoder(c.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func body(c *cobra.Command) (string, error) {
	text, err := input(c)
	return strings.TrimSpace(text), err
}
func input(c *cobra.Command) (string, error) {
	if f, ok := c.InOrStdin().(*os.File); ok {
		st, e := f.Stat()
		if e != nil {
			return "", e
		}
		if st.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}
	return readBody(c.InOrStdin())
}
func readBody(r io.Reader) (string, error) {
	const max = 8 * 1024 * 1024
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil {
		return "", e
	}
	if len(b) > max {
		return "", errors.New("input exceeds 8 MiB")
	}
	return string(b), nil
}
func taskView(t task.Task) any {
	return struct {
		Ref string `json:"ref"`
		task.Task
	}{task.ID(t.ID), t}
}
func (a *application) taskCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task [TSK-n]", Short: "Tasks, comments, sessions, files and live events", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		t, e := a.repo.Task(c.Context(), id)
		if e != nil {
			return e
		}
		expanded, e := app.ExpandTaskContext(c.Context(), a.repo, t, a.v.GetString("agent"))
		if e != nil {
			return e
		}
		return output(c, struct {
			Ref string `json:"ref"`
			task.Task
			Context app.TaskContext `json:"context"`
		}{task.ID(t.ID), t, expanded})
	}}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		tasks, e := a.repo.Tasks(c.Context())
		if e != nil {
			return e
		}
		out := []any{}
		for _, t := range tasks {
			out = append(out, taskView(t))
		}
		return output(c, out)
	}})
	var tags []string
	var status string
	create := &cobra.Command{Use: "create NAME", Short: "Create a task; read description from stdin", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		desc, e := body(c)
		if e != nil {
			return e
		}
		t, e := a.repo.CreateTask(c.Context(), args[0], desc, task.Status(status), tags)
		if e != nil {
			return e
		}
		return output(c, taskView(t))
	}}
	create.Flags().StringSliceVarP(&tags, "tag", "t", nil, "Tag name (repeatable)")
	create.Flags().StringVar(&status, "status", "planning", "Initial status")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{Use: "rm TSK-n", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		return a.repo.RemoveTask(c.Context(), id)
	}})
	cmd.AddCommand(&cobra.Command{Use: "status TSK-n STATUS", Short: "Set planning, todo, validated or closed", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		return a.repo.SetStatus(c.Context(), id, task.Status(args[1]))
	}})
	cmd.AddCommand(&cobra.Command{Use: "comment TSK-n", Short: "Read a comment from stdin", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		s, e := body(c)
		if e != nil {
			return e
		}
		eid, e := a.repo.Comment(c.Context(), id, s)
		if e != nil {
			return e
		}
		return output(c, map[string]any{"event_id": eid})
	}})
	var bindTask string
	bind := &cobra.Command{Use: "bind AGENT_NAME SESSION_ID", Short: "Bind a harness session to --task (or LOOSTER_TASK_ID)", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(bindTask)
		if e != nil {
			return e
		}
		return a.repo.Bind(c.Context(), id, args[0], args[1])
	}}
	bind.Flags().StringVar(&bindTask, "task", os.Getenv("LOOSTER_TASK_ID"), "Task ID (required outside a task environment)")
	cmd.AddCommand(bind)
	var after int64
	var once bool
	var interval time.Duration
	follow := &cobra.Command{Use: "follow [TSK-n]", Short: "Stream task events as NDJSON", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if interval <= 0 {
			return errors.New("interval must be positive")
		}
		var id int64
		var e error
		if len(args) == 1 {
			id, e = task.ParseID(args[0])
			if e != nil {
				return e
			}
		}
		enc := json.NewEncoder(c.OutOrStdout())
		for {
			events, e := a.repo.Events(c.Context(), id, after)
			if e != nil {
				return e
			}
			for _, v := range events {
				if e = enc.Encode(v); e != nil {
					return e
				}
				after = v.ID
			}
			if len(events) == 500 {
				continue
			}
			if once {
				return nil
			}
			if e = pause(c.Context(), interval); e != nil {
				return nil
			}
		}
	}}
	follow.Flags().Int64Var(&after, "after", 0, "Resume after this event ID")
	follow.Flags().BoolVar(&once, "once", false, "Print current events and exit")
	follow.Flags().DurationVar(&interval, "interval", time.Second, "Poll interval")
	cmd.AddCommand(follow)
	var psOnce bool
	var psInterval time.Duration
	var psFormat string
	ps := &cobra.Command{Use: "ps", Short: "Live task and agent session overview", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if psFormat != "table" && psFormat != "json" {
			return errors.New("format must be table or json")
		}
		if psInterval <= 0 {
			return errors.New("interval must be positive")
		}
		for {
			tasks, e := a.repo.Tasks(c.Context())
			if e != nil {
				return e
			}
			sessions, e := a.repo.Sessions(c.Context(), "")
			if e != nil {
				return e
			}
			if !psOnce && psFormat == "table" {
				fmt.Fprint(c.OutOrStdout(), "\033[H\033[2J")
			}
			if psFormat == "json" {
				views := []any{}
				for _, t := range tasks {
					views = append(views, taskView(t))
				}
				e = output(c, map[string]any{"tasks": views, "sessions": sessions})
			} else {
				e = printTaskOverview(c.OutOrStdout(), tasks, sessions)
			}
			if e != nil {
				return e
			}
			if psOnce {
				return nil
			}
			if e = pause(c.Context(), psInterval); e != nil {
				return nil
			}
		}
	}}
	ps.Flags().BoolVar(&psOnce, "once", false, "Print one snapshot")
	ps.Flags().DurationVar(&psInterval, "interval", time.Second, "Refresh interval")
	ps.Flags().StringVar(&psFormat, "format", "table", "Output format: table or json")
	cmd.AddCommand(ps, a.fileCommand())
	return cmd
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (a *application) ownerCommand(kind string) *cobra.Command {
	cmd := &cobra.Command{Use: kind + " [NAME]", Short: "Manage " + kind + "s and memories", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if kind == "tag" {
			t, e := a.repo.Tag(c.Context(), args[0])
			if e != nil {
				return e
			}
			m, e := a.repo.Memories(c.Context(), kind, args[0])
			if e != nil {
				return e
			}
			return output(c, map[string]any{"tag": t, "ref": fmt.Sprintf("TAG-%d", t.ID), "memories": memoryViews(m)})
		}
		v, e := a.repo.Agent(c.Context(), args[0])
		if e != nil {
			return e
		}
		m, e := a.repo.Memories(c.Context(), kind, args[0])
		if e != nil {
			return e
		}
		return output(c, map[string]any{"agent": v, "memories": memoryViews(m)})
	}}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if kind == "tag" {
			r, e := a.repo.Tags(c.Context())
			if e != nil {
				return e
			}
			return output(c, r)
		}
		r, e := a.repo.Agents(c.Context())
		if e != nil {
			return e
		}
		return output(c, r)
	}})
	create := &cobra.Command{Use: "create NAME", Annotations: map[string]string{"human": "true"}, Short: "Read description from stdin", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if kind == "tag" {
			return errors.New("tags are defined only by TAG declarations in a complete behaviour manifest; use looster behaviour apply")
		}
		s, e := body(c)
		if e != nil {
			return e
		}
		if kind == "tag" {
			return a.repo.CreateTag(c.Context(), args[0], s)
		}
		return a.repo.CreateAgent(c.Context(), args[0], s)
	}}

	if kind == "tag" {
		create.Annotations["local"] = "true"
	}
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{Use: "rm NAME", Annotations: map[string]string{"human": "true"}, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if kind == "tag" {
			return a.repo.RemoveTag(c.Context(), args[0])
		}
		return a.repo.RemoveAgent(c.Context(), args[0])
	}})
	mem := &cobra.Command{Use: "memory", Short: "Manage shared context"}
	for _, op := range []string{"add", "rm", "wipe", "list"} {
		op := op
		use := op + " NAME"
		n := 1
		if op == "rm" {
			use += " MEM-n"
			n = 2
		}
		mem.AddCommand(&cobra.Command{Use: use, Args: cobra.ExactArgs(n), RunE: func(c *cobra.Command, args []string) error {
			switch op {
			case "list":
				m, e := a.repo.Memories(c.Context(), kind, args[0])
				if e != nil {
					return e
				}
				return output(c, memoryViews(m))
			case "rm":
				id, e := task.ParsePrettyID(args[1], "MEM")
				if e != nil {
					return e
				}
				return a.repo.RemoveMemory(c.Context(), kind, args[0], id)
			default:
				s, e := body(c)
				if e != nil {
					return e
				}
				return a.repo.AddMemory(c.Context(), kind, args[0], s, op == "wipe")
			}
		}})
	}
	cmd.AddCommand(mem)
	if kind == "agent" {
		cmd.AddCommand(&cobra.Command{Use: "sessions NAME", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			r, e := a.repo.Sessions(c.Context(), args[0])
			if e != nil {
				return e
			}
			return output(c, r)
		}})
	}
	return cmd
}
func (a *application) objects(ctx context.Context) (app.Objects, error) {
	v := a.v
	return objectstore.New(ctx, objectstore.Config{Endpoint: v.GetString("s3.endpoint"), Region: v.GetString("s3.region"), Bucket: v.GetString("s3.bucket"), AccessKey: v.GetString("s3.access_key"), SecretKey: v.GetString("s3.secret_key")})
}
func randomID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (a *application) serveCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "serve", Annotations: map[string]string{"human": "true"}, Short: "Run parallel Jcode workers until interrupted", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v := a.v
		if v.GetString("harness") != "jcode" {
			return errors.New("only the jcode harness is supported")
		}
		objects, e := a.objects(c.Context())
		if e != nil {
			return e
		}
		workdir, e := filepath.Abs(v.GetString("workdir"))
		if e != nil {
			return e
		}
		names := v.GetStringSlice("agents")
		if len(names) == 1 {
			names = strings.FieldsFunc(names[0], func(r rune) bool { return r == ',' || r == ' ' })
		}

		concurrency := v.GetInt("concurrency")
		if concurrency < 1 || concurrency > 128 {
			return errors.New("concurrency must be between 1 and 128")
		}
		if a.store.Pool.Config().MaxConns < int32(concurrency+4) {
			return fmt.Errorf("PostgreSQL pool needs at least %d connections; add pool_max_conns=%d to database_url", concurrency+4, concurrency+4)
		}
		childEnv := map[string]string{}
		for _, key := range []string{"endpoint", "region", "bucket", "access_key", "secret_key"} {
			childEnv["LOOSTER_S3_"+strings.ToUpper(key)] = v.GetString("s3." + key)
		}
		service := app.Service{Log: slog.New(slog.NewTextHandler(c.ErrOrStderr(), nil)), Repo: a.repo, Queue: a.store, Harness: &jcode.Harness{Binary: v.GetString("jcode.binary"), SessionsDir: v.GetString("jcode.sessions_dir"), DatabaseURL: v.GetString("database_url"), Objects: objects, Environment: childEnv}, Objects: objects}
		return service.Serve(c.Context(), app.WorkerConfig{Namespace: v.GetString("namespace"), Harness: v.GetString("harness"), ID: randomID(), Agents: names, Concurrency: concurrency, Workdir: workdir, Poll: v.GetDuration("poll_interval"), RunTimeout: v.GetDuration("run_timeout")})
	}}
	return cmd
}
func (a *application) fileCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "file", Short: "S3-backed task and comment attachments"}
	var comment int64
	add := &cobra.Command{Use: "add TSK-n PATH", Short: "Upload a file (optionally attach to --comment EVENT_ID)", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		if _, e = a.repo.Task(c.Context(), id); e != nil {
			return e
		}
		f, e := os.Open(args[1])
		if e != nil {
			return e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return errors.New("attachment must be a regular file")
		}
		o, e := a.objects(c.Context())
		if e != nil {
			return e
		}
		key := fmt.Sprintf("tasks/%s/%s/%s", task.ID(id), randomID(), filepath.Base(args[1]))
		if name := a.v.GetString("namespace"); name != namespace.Default {
			key = "namespaces/" + name + "/" + key
		}
		if e = o.Put(c.Context(), key, f, st.Size()); e != nil {
			return e
		}
		att := task.Attachment{TaskID: id, Name: filepath.Base(args[1]), Key: key, Size: st.Size()}
		if comment > 0 {
			att.EventID = &comment
		}
		att, e = a.repo.AddAttachment(c.Context(), att)
		if e != nil {
			return fmt.Errorf("file uploaded as %s but metadata failed: %w", key, e)
		}
		return output(c, att)
	}}
	add.Flags().Int64Var(&comment, "comment", 0, "Comment event ID")
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{Use: "list TSK-n", Aliases: []string{"ls"}, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		r, e := a.repo.Attachments(c.Context(), id)
		if e != nil {
			return e
		}
		return output(c, r)
	}})
	cmd.AddCommand(&cobra.Command{Use: "get TSK-n FILE_ID OUTPUT", Short: "Download an attachment; OUTPUT '-' writes to stdout", Args: cobra.ExactArgs(3), RunE: func(c *cobra.Command, args []string) error {
		id, e := task.ParseID(args[0])
		if e != nil {
			return e
		}
		fileID, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil || fileID < 1 {
			return errors.New("invalid file ID")
		}
		all, e := a.repo.Attachments(c.Context(), id)
		if e != nil {
			return e
		}
		var key string
		for _, f := range all {
			if f.ID == fileID {
				key = f.Key
			}
		}
		if key == "" {
			return errors.New("attachment not found on this task")
		}
		o, e := a.objects(c.Context())
		if e != nil {
			return e
		}
		r, e := o.Get(c.Context(), key)
		if e != nil {
			return e
		}
		defer r.Close()
		if args[2] == "-" {
			_, e = io.Copy(c.OutOrStdout(), r)
			return e
		}
		f, e := os.OpenFile(args[2], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(f, r)
		closeErr := f.Close()
		if copyErr != nil {
			_ = os.Remove(args[2])
			return copyErr
		}
		return closeErr
	}})
	return cmd
}
