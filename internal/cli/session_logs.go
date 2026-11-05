package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
	"github.com/spf13/cobra"
)

func (a *application) sessionLogCommand() *cobra.Command {
	var job, sid, name, taskRef string
	cmd := &cobra.Command{Use: "log KIND BODY", Short: "Append an immutable question, blocker or other observation to a proven session run", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(job, 10, 64)
		if err != nil || id < 1 {
			return fmt.Errorf("--job or LOOSTER_JOB_ID must identify a positive queued job")
		}
		var taskID int64
		if taskRef != "" {
			taskID, err = task.ParseID(taskRef)
			if err != nil {
				return err
			}
		}
		repo, ok := a.repo.(app.SessionLogRepository)
		if !ok {
			return app.ErrSessionHistoryUnavailable
		}
		log, err := repo.AppendSessionLog(c.Context(), agent.SessionLogInput{JobID: id, SessionID: sid, Agent: name, TaskID: taskID, Kind: args[0], Body: args[1]})
		if err != nil {
			return err
		}
		return output(c, log)
	}}
	cmd.Flags().StringVar(&job, "job", os.Getenv("LOOSTER_JOB_ID"), "Exact queued job ID (defaults to current run)")
	cmd.Flags().StringVar(&sid, "session", os.Getenv("LOOSTER_SESSION_ID"), "Optional expected harness session ID")
	cmd.Flags().StringVar(&name, "agent", os.Getenv("LOOSTER_AGENT"), "Agent owning the run")
	cmd.Flags().StringVar(&taskRef, "task", os.Getenv("LOOSTER_TASK_ID"), "Optional expected task ID, e.g. TSK-1")
	return cmd
}
