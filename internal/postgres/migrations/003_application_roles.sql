-- +goose Up
-- Upgrade databases that used PostgreSQL role/RLS authorization. Existing login
-- roles are left intact: they may be used by other applications or databases.
DROP POLICY IF EXISTS event_read ON events;
DROP POLICY IF EXISTS event_write ON events;
DROP POLICY IF EXISTS memory_read ON memories;
DROP POLICY IF EXISTS memory_write ON memories;
DROP POLICY IF EXISTS session_read ON sessions;
DROP POLICY IF EXISTS session_write ON sessions;
ALTER TABLE events DISABLE ROW LEVEL SECURITY;
ALTER TABLE memories DISABLE ROW LEVEL SECURITY;
ALTER TABLE sessions DISABLE ROW LEVEL SECURITY;
ALTER TABLE agents DROP COLUMN IF EXISTS db_role;
ALTER FUNCTION looster_event_id() SECURITY INVOKER;
ALTER FUNCTION looster_task_event() SECURITY INVOKER;
ALTER FUNCTION looster_dispatch() SECURITY INVOKER;
ALTER FUNCTION looster_schedule(text[]) SECURITY INVOKER;
GRANT EXECUTE ON FUNCTION looster_event_id(),looster_task_event(),looster_dispatch(),looster_schedule(text[]) TO PUBLIC;
-- Append-only logging is a data invariant independent of human/agent roles.
-- +goose StatementBegin
CREATE FUNCTION looster_immutable_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Looster events are append-only';
END; $$;
CREATE TRIGGER immutable_event BEFORE UPDATE OR DELETE ON events
 FOR EACH ROW EXECUTE FUNCTION looster_immutable_event();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER immutable_event ON events;
DROP FUNCTION looster_immutable_event();
-- Legacy database authorization is deliberately not restored.
