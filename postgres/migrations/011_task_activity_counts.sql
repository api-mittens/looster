-- +goose Up
CREATE INDEX events_task_activity_idx ON events(task_id,kind)
 WHERE kind IN ('status_changed','comment');

-- +goose Down
DROP INDEX events_task_activity_idx;
