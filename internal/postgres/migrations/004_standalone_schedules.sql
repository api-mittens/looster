-- +goose Up
-- A scheduled ping is an ordinary durable job with no task attached.
ALTER TABLE rules DROP CONSTRAINT rules_schedule_check;
ALTER TABLE rules DROP COLUMN task_id;
ALTER TABLE rules ADD CONSTRAINT rules_schedule_check CHECK (
 (trigger='every' AND source IS NULL AND interval_seconds BETWEEN 1 AND 9223372036 AND length(trim(prompt))>0 AND next_run_at IS NOT NULL)
 OR (trigger<>'every' AND interval_seconds=0 AND prompt='' AND next_run_at IS NULL)
);
ALTER TABLE jobs ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE events ALTER COLUMN task_id DROP NOT NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION looster_schedule(agent_names text[]) RETURNS integer
LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
DECLARE
 r rules%ROWTYPE;
 tick timestamptz := statement_timestamp();
 scheduled_event bigint;
 dispatched integer := 0;
BEGIN
 FOR r IN SELECT * FROM rules
  WHERE trigger='every' AND target=ANY(agent_names) AND next_run_at<=tick
  ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED
 LOOP
  UPDATE rules SET next_run_at=r.next_run_at+
   ((floor(extract(epoch FROM (tick-r.next_run_at))/r.interval_seconds)::bigint+1)*r.interval_seconds)*interval '1 second'
   WHERE id=r.id;
  IF EXISTS(SELECT FROM jobs WHERE schedule_rule_id=r.id AND state IN ('pending','running','archiving')) THEN
   CONTINUE;
  END IF;
  INSERT INTO events(kind,agent_name,body)
   VALUES('scheduled',r.target,r.prompt) RETURNING id INTO scheduled_event;
  INSERT INTO jobs(agent_name,event_id,prompt,schedule_rule_id)
   VALUES(r.target,scheduled_event,r.prompt,r.id);
  dispatched := dispatched+1;
 END LOOP;
 RETURN dispatched;
END; $$;
-- +goose StatementEnd

-- +goose Down
-- There is no lossless way to attach standalone jobs/logs to historical tasks.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Standalone schedules cannot be rolled back without restoring a backup';
END; $$;
-- +goose StatementEnd
