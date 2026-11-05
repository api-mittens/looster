-- +goose Up
CREATE TABLE session_logs (
 id bigserial PRIMARY KEY,
 job_id bigint NOT NULL CHECK(job_id>0),
 session_id text NOT NULL CHECK(btrim(session_id)<>''),
 agent_name text NOT NULL,
 task_id bigint NOT NULL DEFAULT 0,
 kind text NOT NULL CHECK(btrim(kind)<>''),
 body text NOT NULL CHECK(btrim(body)<>''),
 created_at timestamptz NOT NULL DEFAULT now()
);
-- No mutable foreign keys: history survives task, agent and job removal.
CREATE INDEX session_logs_task_idx ON session_logs(task_id,id);
CREATE INDEX session_logs_job_idx ON session_logs(job_id,id);
CREATE TRIGGER immutable_session_log BEFORE UPDATE OR DELETE ON session_logs
 FOR EACH ROW EXECUTE FUNCTION looster_immutable_event();

-- +goose Down
DROP TABLE session_logs;
