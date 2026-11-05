-- +goose Up
-- Support indexed latest-activity lookup without emitting comment notifications.
CREATE INDEX session_logs_task_last_activity ON session_logs(task_id,created_at DESC);

-- +goose Down
DROP INDEX session_logs_task_last_activity;
