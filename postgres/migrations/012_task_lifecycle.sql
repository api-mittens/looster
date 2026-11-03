-- +goose Up
-- Preserve work awaiting review as open work, not as implicitly approved work.
-- Do not replay workflow events/jobs as a side effect of upgrading a database.
ALTER TABLE tasks DISABLE TRIGGER task_event;
UPDATE tasks SET status='todo' WHERE status='unvalidated';
ALTER TABLE tasks ENABLE TRIGGER task_event;
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK(status IN ('planning','todo','validated','closed'));
-- Existing rules remain usable against the replacement open-work status.
UPDATE rules SET trigger='todo' WHERE trigger='unvalidated';
UPDATE rules SET trigger='stop_todo' WHERE trigger='stop_unvalidated';
ALTER TABLE rules DROP CONSTRAINT rules_trigger_check;
ALTER TABLE rules ADD CONSTRAINT rules_trigger_check CHECK(trigger IN ('planning','todo','validated','closed','completed','every','stop_planning','stop_todo','stop_validated','stop_closed'));
-- Historical append-only events deliberately keep their original bodies.

-- +goose Down
-- The removed status cannot be reconstructed without guessing which TODO tasks
-- were previously awaiting review. Never silently rewrite user work on rollback.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Task lifecycle changes require a backup restore to roll back safely';
END; $$;
-- +goose StatementEnd
