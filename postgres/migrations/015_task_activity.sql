-- +goose Up
-- Latest activity is derived from existing history, not a mutable task column.
-- Keep board refreshes cheap even when sessions produce many output events.
CREATE INDEX events_task_last_activity ON events (task_id, created_at DESC)
WHERE task_id IS NOT NULL;

-- +goose Down
DROP INDEX events_task_last_activity;
