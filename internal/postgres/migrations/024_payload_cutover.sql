-- +goose Up
-- A namespace-local durable fence, not a connection-local session setting.
CREATE TABLE payload_cutover (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 enabled boolean NOT NULL DEFAULT false
);
INSERT INTO payload_cutover(singleton,enabled) VALUES(true,false);

-- +goose StatementBegin
CREATE FUNCTION looster_require_payload_reference() RETURNS trigger
LANGUAGE plpgsql SET search_path=public,pg_temp AS $$
DECLARE
 active boolean;
 check_body boolean := false;
 check_history boolean := false;
 check_summary boolean := false;
 check_error boolean := false;
BEGIN
 IF TG_TABLE_NAME='events' THEN
  IF NEW.kind NOT IN ('agent_output','agent_failed','agent_interrupted','system','worker_started','worker_stopped','worker_error','worker_start_failed','scheduler_failed','scheduler_dispatched') THEN
   RETURN NEW;
  END IF;
  IF TG_OP='INSERT' THEN check_body:=true;
  ELSE check_body:=NEW.body IS DISTINCT FROM OLD.body OR NEW.kind IS DISTINCT FROM OLD.kind;
  END IF;
 ELSIF TG_TABLE_NAME='session_logs' THEN
  IF TG_OP='INSERT' THEN check_body:=true;
  ELSE check_body:=NEW.body IS DISTINCT FROM OLD.body;
  END IF;
 ELSE
  IF TG_OP='INSERT' THEN
   check_summary:=true; check_error:=true; check_history:=TG_TABLE_NAME='jobs';
  ELSE
   check_summary:=NEW.summary IS DISTINCT FROM OLD.summary;
   check_error:=NEW.error IS DISTINCT FROM OLD.error;
   IF TG_TABLE_NAME='jobs' THEN check_history:=NEW.history IS DISTINCT FROM OLD.history; END IF;
  END IF;
 END IF;
 IF NOT (check_body OR check_history OR check_summary OR check_error) THEN RETURN NEW; END IF;
 -- SHARE row locks serialize every payload writer with EnablePayloadCutover's
 -- UPDATE, including transactions begun before cutover. Waiting writers see the
 -- updated row (or fail serialization), never an obsolete disabled fence.
 SELECT enabled INTO STRICT active FROM payload_cutover WHERE singleton FOR SHARE;
 IF NOT active THEN RETURN NEW; END IF;
 IF check_body THEN
  IF NEW.body<>'' AND NEW.body !~ '^looster-payload:v1:[0-9a-f]{64}$' THEN
   RAISE EXCEPTION 'payload cutover requires content-addressed %.body',TG_TABLE_NAME;
  END IF;
 END IF;
 IF check_summary THEN
  IF NEW.summary<>'' AND NEW.summary !~ '^looster-payload:v1:[0-9a-f]{64}$' THEN
   RAISE EXCEPTION 'payload cutover requires content-addressed %.summary',TG_TABLE_NAME;
  END IF;
 END IF;
 IF check_error THEN
  IF NEW.error<>'' AND NEW.error !~ '^looster-payload:v1:[0-9a-f]{64}$' THEN
   RAISE EXCEPTION 'payload cutover requires content-addressed %.error',TG_TABLE_NAME;
  END IF;
 END IF;
 IF check_history THEN
  IF octet_length(NEW.history)>0 AND encode(NEW.history,'escape') !~ '^looster-payload:v1:[0-9a-f]{64}$' THEN
   RAISE EXCEPTION 'payload cutover requires content-addressed jobs.history';
  END IF;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER payload_cutover_events BEFORE INSERT OR UPDATE ON events FOR EACH ROW EXECUTE FUNCTION looster_require_payload_reference();
CREATE TRIGGER payload_cutover_logs BEFORE INSERT OR UPDATE ON session_logs FOR EACH ROW EXECUTE FUNCTION looster_require_payload_reference();
CREATE TRIGGER payload_cutover_jobs BEFORE INSERT OR UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION looster_require_payload_reference();
CREATE TRIGGER payload_cutover_runs BEFORE INSERT OR UPDATE ON agent_session_runs FOR EACH ROW EXECUTE FUNCTION looster_require_payload_reference();

-- +goose Down
-- Never silently re-enable old writers after references have been activated.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM payload_cutover WHERE enabled) THEN
  RAISE EXCEPTION 'Cannot remove an active payload cutover fence';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TRIGGER payload_cutover_events ON events;
DROP TRIGGER payload_cutover_logs ON session_logs;
DROP TRIGGER payload_cutover_jobs ON jobs;
DROP TRIGGER payload_cutover_runs ON agent_session_runs;
DROP FUNCTION looster_require_payload_reference();
DROP TABLE payload_cutover;
