package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/dominikbb/looster/internal/agent"
	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/event"
	"github.com/dominikbb/looster/internal/task"
)

type logOptions struct {
	follow, json       bool
	tail               int
	after              int64
	task, agent, since string
	kinds              []string
	interval           time.Duration
	filter             event.Filter
	resume             bool
}

func (a *application) logsCommand() *cobra.Command {
	o := &logOptions{}
	cmd := &cobra.Command{Use: "logs", Aliases: []string{"log"}, Short: "Show recent system events, scheduled runs, and agent/task activity", Args: func(c *cobra.Command, args []string) error {
		if err := cobra.NoArgs(c, args); err != nil {
			return err
		}
		return o.prepare(c)
	}, RunE: func(c *cobra.Command, _ []string) error { return o.run(c, a.repo) }}
	f := cmd.Flags()
	f.BoolVarP(&o.follow, "follow", "f", false, "Follow new events until interrupted")
	f.IntVarP(&o.tail, "tail", "n", 100, "Number of recent matching events (-1 for all, 0 for new events only)")
	f.Int64Var(&o.after, "after", 0, "Replay every matching event after this event ID")
	f.StringVar(&o.task, "task", "", "Filter to a task, e.g. TSK-1")
	f.StringVar(&o.agent, "agent", "", "Filter to agent activity, including changes made by that agent")
	f.StringSliceVar(&o.kinds, "kind", nil, "Event kinds (repeatable or comma-separated)")
	f.StringVar(&o.since, "since", "", "Only events since a duration ago (10m, 2h) or an RFC3339 timestamp")
	f.DurationVar(&o.interval, "interval", time.Second, "Follow polling interval")
	f.BoolVar(&o.json, "json", false, "Emit original event records as NDJSON")
	return cmd
}

func (o *logOptions) prepare(c *cobra.Command) error {
	if o.tail < -1 || o.tail > 10000 {
		return errors.New("tail must be -1 or between 0 and 10000")
	}
	if o.after < 0 {
		return errors.New("after must not be negative")
	}
	if o.interval <= 0 {
		return errors.New("interval must be positive")
	}
	if c.Flags().Changed("after") && c.Flags().Changed("tail") {
		return errors.New("choose --after or --tail, not both")
	}
	o.resume = c.Flags().Changed("after")
	o.filter = event.Filter{Agent: o.agent, Kinds: o.kinds}
	if o.task != "" {
		id, err := task.ParseID(o.task)
		if err != nil {
			return err
		}
		o.filter.TaskID = id
	}
	if o.agent != "" && !agent.ValidName(o.agent) {
		return errors.New("invalid agent name")
	}
	for i, kind := range o.kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return errors.New("event kinds must not be empty")
		}
		o.kinds[i] = kind
	}
	since, err := logSince(o.since, time.Now())
	if err != nil {
		return err
	}
	o.filter.Since = since
	return nil
}
func logSince(value string, now time.Time) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return nil, errors.New("since duration must be positive")
		}
		t := now.Add(-d)
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, errors.New("since must be a positive duration (10m, 2h) or an RFC3339 timestamp")
	}
	return &t, nil
}

func (o *logOptions) run(c *cobra.Command, repo app.Repository) error {
	ctx := c.Context()
	emit := func(e event.Event) error { return writeLog(c.OutOrStdout(), e, o.json) }
	high, err := logCursor(ctx, repo)
	if err != nil {
		return logContextError(ctx, err)
	}
	cursor := int64(0)
	count := 0
	if o.resume || o.tail == -1 {
		cursor = o.after
		count, err = readLogsThrough(ctx, repo, o.filter, &cursor, high, emit)
	} else {
		cursor = high
		if high > 0 && o.tail > 0 {
			filter := o.filter
			filter.Until = high
			filter.Limit = o.tail
			filter.Desc = true
			var events []event.Event
			events, err = repo.Logs(ctx, filter)
			if err == nil {
				for i := len(events) - 1; i >= 0; i-- {
					if err = emit(events[i]); err != nil {
						return err
					}
					count++
				}
			}
		}
	}
	if err != nil {
		return logContextError(ctx, err)
	}
	if count == 0 && (o.follow || o.tail != 0) {
		message := "No matching events."
		if o.follow {
			message = "No matching events; waiting for new events..."
			if o.tail == 0 && !o.resume {
				message = "Waiting for new events..."
			}
		}
		if _, err = fmt.Fprintln(c.ErrOrStderr(), message); err != nil {
			return err
		}
	}
	if !o.follow {
		return nil
	}
	for {
		if pause(ctx, o.interval) != nil {
			return nil
		}
		high, err = logCursor(ctx, repo)
		if err != nil {
			return logContextError(ctx, err)
		}
		if _, err = readLogsThrough(ctx, repo, o.filter, &cursor, high, emit); err != nil {
			return logContextError(ctx, err)
		}
	}
}

// Bound each read by a committed high-water mark, so sparse filters advance
// without rescanning old nonmatching rows or losing a concurrent insertion.
func logCursor(ctx context.Context, repo app.Repository) (int64, error) {
	rows, err := repo.Logs(ctx, event.Filter{Limit: 1, Desc: true})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].ID, nil
}
func readLogsThrough(ctx context.Context, repo app.Repository, filter event.Filter, cursor *int64, high int64, emit func(event.Event) error) (int, error) {
	count := 0
	for *cursor < high {
		filter.After = *cursor
		filter.Until = high
		filter.Limit = 500
		filter.Desc = false
		rows, err := repo.Logs(ctx, filter)
		if err != nil {
			return count, err
		}
		for _, row := range rows {
			if err = emit(row); err != nil {
				return count, err
			}
			count++
			*cursor = row.ID
		}
		if len(rows) < filter.Limit {
			*cursor = high
			break
		}
	}
	return count, nil
}
func logContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func writeLog(w io.Writer, e event.Event, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(e)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%d %s", e.CreatedAt.Format(time.RFC3339), e.ID, e.Kind)
	if e.Agent != "" {
		fmt.Fprintf(&b, " agent=%s", e.Agent)
	}
	if e.ActorAgent != "" && e.ActorAgent != e.Agent {
		fmt.Fprintf(&b, " actor=%s", e.ActorAgent)
	}
	if e.TaskID != nil {
		fmt.Fprintf(&b, " task=%s", task.ID(*e.TaskID))
	}
	b.WriteByte('\n')
	body := readableLogBody(e)
	if body != "" {
		// Keep terminal control sequences in agent output from changing the display.
		body = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return '�'
			}
			return r
		}, body)
		b.WriteString("  ")
		b.WriteString(strings.ReplaceAll(strings.TrimRight(body, "\n"), "\n", "\n  "))
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}
func readableLogBody(e event.Event) string {
	if e.Kind != "agent_output" {
		return e.Body
	}
	var out struct {
		Type, Text, Delta, Name, Tool, Message string
		SessionID                              string `json:"session_id"`
	}
	if json.Unmarshal([]byte(e.Body), &out) != nil || out.Type == "" {
		return e.Body
	}
	text := out.Text
	if text == "" {
		text = out.Delta
	}
	if text == "" {
		text = out.Message
	}
	if text != "" {
		return out.Type + ": " + text
	}
	if out.Name != "" {
		return out.Type + ": " + out.Name
	}
	if out.Tool != "" {
		return out.Type + ": " + out.Tool
	}
	if out.SessionID != "" {
		return out.Type + " session=" + out.SessionID
	}
	return e.Body
}
