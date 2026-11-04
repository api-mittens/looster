-- +goose Up
-- Staging belongs to the mutable job so capture and archive retries survive a
-- worker restart. Final records have no cascading references to mutable rows.
ALTER TABLE jobs ADD COLUMN started_at timestamptz;
ALTER TABLE jobs ADD COLUMN summary text NOT NULL DEFAULT '';
CREATE TABLE agent_session_runs (
 job_id bigint PRIMARY KEY,
 task_id bigint NOT NULL DEFAULT 0,
 context_task_id bigint NOT NULL DEFAULT 0,
 agent_name text NOT NULL,
 session_id text NOT NULL DEFAULT '',
 started_at timestamptz NOT NULL,
 finished_at timestamptz NOT NULL DEFAULT now(),
 outcome text NOT NULL CHECK(outcome IN ('done','failed','interrupted')),
 summary text NOT NULL DEFAULT '',
 summary_status text NOT NULL CHECK(summary_status IN ('available','missing','failed','interrupted')),
 error text NOT NULL DEFAULT ''
);
CREATE INDEX agent_session_runs_agent_idx ON agent_session_runs(agent_name,job_id DESC);
CREATE INDEX agent_session_runs_task_idx ON agent_session_runs(task_id,job_id DESC);
-- +goose StatementBegin
CREATE FUNCTION looster_immutable_session_run() RETURNS trigger LANGUAGE plpgsql SET search_path=public,pg_temp AS $$
BEGIN
 RAISE EXCEPTION 'Looster session runs are append-only';
END; $$;
CREATE TRIGGER immutable_session_run BEFORE UPDATE OR DELETE ON agent_session_runs
 FOR EACH ROW EXECUTE FUNCTION looster_immutable_session_run();
-- +goose StatementEnd

-- +goose Down
DROP TABLE agent_session_runs;
DROP FUNCTION looster_immutable_session_run();
ALTER TABLE jobs DROP COLUMN started_at, DROP COLUMN summary;
