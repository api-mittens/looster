-- +goose Up
CREATE TABLE tags (id bigserial PRIMARY KEY, name text UNIQUE NOT NULL CHECK(name ~ '^[A-Za-z-]+(/[A-Za-z-]+)*$'), description text NOT NULL);
CREATE TABLE agents (name text PRIMARY KEY CHECK(name ~ '^[A-Za-z][A-Za-z0-9_-]*$'), description text NOT NULL);
CREATE TABLE tasks (id bigserial PRIMARY KEY, name text NOT NULL CHECK(length(trim(name))>0), description text NOT NULL, status text NOT NULL DEFAULT 'planning' CHECK(status IN ('planning','todo','unvalidated','validated')), created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE task_tags (task_id bigint REFERENCES tasks ON DELETE CASCADE, tag_id bigint REFERENCES tags ON DELETE CASCADE, PRIMARY KEY(task_id,tag_id));
CREATE TABLE memories (id bigserial PRIMARY KEY, tag_id bigint REFERENCES tags ON DELETE CASCADE, agent_name text REFERENCES agents ON DELETE CASCADE, body text NOT NULL CHECK(length(trim(body))>0), created_at timestamptz NOT NULL DEFAULT now(), CHECK ((tag_id IS NULL) <> (agent_name IS NULL)));
CREATE TABLE sessions (task_id bigint REFERENCES tasks ON DELETE CASCADE, agent_name text REFERENCES agents ON DELETE CASCADE, session_id text NOT NULL DEFAULT '', status text NOT NULL DEFAULT 'idle' CHECK(status IN ('idle','running','failed')), PRIMARY KEY(task_id,agent_name));
CREATE TABLE events (id bigserial PRIMARY KEY, task_id bigint NOT NULL, kind text NOT NULL, agent_name text NOT NULL DEFAULT '', body text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX events_task_id_idx ON events(task_id,id);
-- Allocate event IDs under a transaction lock so --after cannot skip a late
-- commit with a smaller ID. Sequence defaults are evaluated before triggers.
ALTER TABLE events ALTER COLUMN id DROP DEFAULT;
-- +goose StatementBegin
CREATE FUNCTION looster_event_id() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('looster:event-sequence',0));
 NEW.id := nextval('events_id_seq');
 RETURN NEW;
END; $$;
CREATE TRIGGER event_id BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION looster_event_id();
-- +goose StatementEnd
CREATE TABLE rules (id bigserial PRIMARY KEY, trigger text NOT NULL CHECK(trigger IN ('todo','completed')), source text REFERENCES agents ON DELETE CASCADE, target text NOT NULL REFERENCES agents ON DELETE CASCADE);
CREATE TABLE jobs (id bigserial PRIMARY KEY, task_id bigint NOT NULL REFERENCES tasks ON DELETE CASCADE, agent_name text NOT NULL REFERENCES agents ON DELETE CASCADE, event_id bigint NOT NULL REFERENCES events, state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','archiving','done','failed')), session_id text NOT NULL DEFAULT '', history bytea, available_at timestamptz NOT NULL DEFAULT now(), error text NOT NULL DEFAULT '', UNIQUE(event_id,agent_name));
CREATE INDEX jobs_pending_idx ON jobs(id) WHERE state IN ('pending','running','archiving');
CREATE TABLE attachments (id bigserial PRIMARY KEY, task_id bigint NOT NULL REFERENCES tasks ON DELETE CASCADE, event_id bigint REFERENCES events, name text NOT NULL, object_key text UNIQUE NOT NULL, size bigint NOT NULL CHECK(size>=0));
CREATE TABLE workers (id text PRIMARY KEY, agents text[] NOT NULL, last_seen timestamptz NOT NULL DEFAULT now());
-- Events and dispatch are committed with the originating mutation, including direct SQL writes.
-- +goose StatementBegin
CREATE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
BEGIN
 INSERT INTO jobs(task_id,agent_name,event_id)
 SELECT NEW.task_id,r.target,NEW.id FROM rules r
 WHERE (NEW.kind='status_changed' AND NEW.body='todo' AND r.trigger='todo')
 OR (NEW.kind='agent_stopped' AND r.trigger='completed' AND r.source=NEW.agent_name)
 ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$;
CREATE TRIGGER dispatch AFTER INSERT ON events FOR EACH ROW EXECUTE FUNCTION looster_dispatch();
CREATE FUNCTION looster_task_event() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  INSERT INTO events(task_id,kind,body) VALUES(OLD.id,'removed',OLD.name); RETURN OLD;
 ELSIF TG_OP='INSERT' THEN
  INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'created',NEW.name);
  IF NEW.status='todo' THEN INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed','todo'); END IF;
 ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
  INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER task_event AFTER INSERT OR UPDATE OR DELETE ON tasks FOR EACH ROW EXECUTE FUNCTION looster_task_event();
-- +goose StatementEnd
-- +goose Down
DROP TABLE workers,attachments,jobs,rules,events,sessions,memories,task_tags,tasks,agents,tags CASCADE;
DROP FUNCTION looster_dispatch(),looster_task_event(),looster_event_id();
