-- +goose Up
-- Historical payload maintenance is opt-in and may only replace bytes with
-- their exact SHA-256 reference. Metadata and ordinary append-only guarantees
-- remain intact. Application backfill rereads remote bytes before this UPDATE.
-- Stop old writers before running backfill: old binaries cannot hydrate refs.
-- This is NOT permission to delete tables, payload columns or remote objects.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION looster_immutable_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND current_setting('looster.payload_backfill',true)='on' THEN
  IF to_jsonb(NEW)-'body'=to_jsonb(OLD)-'body'
   AND NEW.body='looster-payload:v1:' || encode(sha256(convert_to(OLD.body,'UTF8')),'hex') THEN
   IF TG_TABLE_NAME='session_logs' THEN
    RETURN NEW;
   ELSIF TG_TABLE_NAME='events' AND OLD.kind IN ('agent_output','system','worker_started','worker_stopped','worker_error','worker_start_failed','scheduler_failed','scheduler_dispatched') THEN
    RETURN NEW;
   END IF;
  END IF;
 END IF;
 RAISE EXCEPTION 'Looster events are append-only';
END; $$;

CREATE OR REPLACE FUNCTION looster_immutable_session_run() RETURNS trigger LANGUAGE plpgsql SET search_path=public,pg_temp AS $$
BEGIN
 IF TG_OP='UPDATE' AND current_setting('looster.payload_backfill',true)='on' THEN
  IF to_jsonb(NEW)-'summary'-'error'=to_jsonb(OLD)-'summary'-'error'
   AND (NEW.summary=OLD.summary OR NEW.summary='looster-payload:v1:' || encode(sha256(convert_to(OLD.summary,'UTF8')),'hex'))
   AND (NEW.error=OLD.error OR NEW.error='looster-payload:v1:' || encode(sha256(convert_to(OLD.error,'UTF8')),'hex')) THEN
   RETURN NEW;
  END IF;
 END IF;
 RAISE EXCEPTION 'Looster session runs are append-only';
END; $$;
-- +goose StatementEnd

-- +goose Down
-- Reinstating stricter triggers never removes archived data or references.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION looster_immutable_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Looster events are append-only';
END; $$;
CREATE OR REPLACE FUNCTION looster_immutable_session_run() RETURNS trigger LANGUAGE plpgsql SET search_path=public,pg_temp AS $$
BEGIN
 RAISE EXCEPTION 'Looster session runs are append-only';
END; $$;
-- +goose StatementEnd
