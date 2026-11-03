-- +goose Up
-- Deleted-task notifications retain their original task reference without a FK.
ALTER TABLE jobs ADD COLUMN context_task_id bigint;
ALTER TABLE jobs ADD COLUMN notification boolean NOT NULL DEFAULT false;
ALTER TABLE events ADD COLUMN actor_agent text NOT NULL DEFAULT '';
-- +goose StatementBegin
CREATE FUNCTION looster_event_actor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.actor_agent := coalesce(nullif(NEW.actor_agent,''),current_setting('looster.actor_agent',true),'');
 RETURN NEW;
END; $$;
CREATE TRIGGER event_actor BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION looster_event_actor();
CREATE OR REPLACE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
DECLARE message text;
BEGIN
 IF NEW.task_id IS NULL THEN RETURN NEW; END IF;
 message := 'Task TSK-'||NEW.task_id||' changed ('||NEW.kind||'):'||E'\n'||NEW.body;
 -- Workflow pings take precedence if the same event also notifies a session.
 IF NEW.kind<>'removed' AND EXISTS(SELECT FROM tasks WHERE id=NEW.task_id) THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
  WHERE ((NEW.kind='status_changed' AND NEW.body='todo' AND r.trigger='todo')
  OR (NEW.kind='agent_stopped' AND r.trigger='completed' AND r.source=NEW.agent_name))
  AND r.target<>NEW.actor_agent
  ON CONFLICT DO NOTHING;
 END IF;
 -- Never fan out lifecycle/output/notification-delivery events.
 IF NEW.kind IN ('updated','status_changed','comment','tags_changed','attachment_added','attachment_removed','attachment_updated','removed') THEN
  IF NEW.kind='removed' THEN
   INSERT INTO jobs(context_task_id,agent_name,event_id,prompt,session_id,notification)
   SELECT NEW.task_id,s.agent_name,NEW.id,message||E'\nThis task has been deleted; do not continue work on it.',s.session_id,true
   FROM sessions s WHERE s.task_id=NEW.task_id AND s.session_id<>'' AND s.agent_name<>NEW.actor_agent
   ON CONFLICT DO NOTHING;
  ELSE
   INSERT INTO jobs(task_id,agent_name,event_id,prompt,notification)
   SELECT NEW.task_id,s.agent_name,NEW.id,message,true FROM sessions s WHERE s.task_id=NEW.task_id AND s.agent_name<>NEW.actor_agent
   ON CONFLICT DO NOTHING;
  END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE OR REPLACE FUNCTION looster_task_event() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
BEGIN
 IF TG_OP='DELETE' THEN RETURN OLD;
 ELSIF TG_OP='INSERT' THEN
  INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'created',NEW.name);
  IF NEW.status='todo' THEN INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed','todo'); END IF;
 ELSIF NEW IS DISTINCT FROM OLD THEN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
  ELSE
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'updated',NEW.name||E'\n'||NEW.description);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE FUNCTION looster_task_removing() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO events(task_id,kind,body) VALUES(OLD.id,'removed',OLD.name);
 RETURN OLD;
END; $$;
CREATE TRIGGER task_removing BEFORE DELETE ON tasks FOR EACH ROW EXECUTE FUNCTION looster_task_removing();
CREATE FUNCTION looster_tag_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE tid bigint;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 tid := CASE WHEN TG_OP='DELETE' THEN OLD.task_id ELSE NEW.task_id END;
 IF EXISTS(SELECT FROM tasks WHERE id=tid) THEN
  INSERT INTO events(task_id,kind,body) VALUES(tid,'tags_changed','Task tags changed.');
 END IF;
 IF TG_OP='UPDATE' AND OLD.task_id<>NEW.task_id AND EXISTS(SELECT FROM tasks WHERE id=OLD.task_id) THEN
  INSERT INTO events(task_id,kind,body) VALUES(OLD.task_id,'tags_changed','Task tags changed.');
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER task_tag_change AFTER INSERT OR UPDATE OR DELETE ON task_tags FOR EACH ROW EXECUTE FUNCTION looster_tag_change();
CREATE FUNCTION looster_attachment_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE tid bigint; filename text; event_kind text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 tid := CASE WHEN TG_OP='DELETE' THEN OLD.task_id ELSE NEW.task_id END;
 filename := CASE WHEN TG_OP='DELETE' THEN OLD.name ELSE NEW.name END;
 event_kind := CASE TG_OP WHEN 'DELETE' THEN 'attachment_removed' WHEN 'INSERT' THEN 'attachment_added' ELSE 'attachment_updated' END;
 IF EXISTS(SELECT FROM tasks WHERE id=tid) THEN
  INSERT INTO events(task_id,kind,body) VALUES(tid,event_kind,filename);
 END IF;
 IF TG_OP='UPDATE' AND OLD.task_id<>NEW.task_id AND EXISTS(SELECT FROM tasks WHERE id=OLD.task_id) THEN
  INSERT INTO events(task_id,kind,body) VALUES(OLD.task_id,'attachment_removed',OLD.name);
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER attachment_change AFTER INSERT OR UPDATE OR DELETE ON attachments FOR EACH ROW EXECUTE FUNCTION looster_attachment_change();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Task notification migration requires a backup restore to roll back safely';
END; $$;
-- +goose StatementEnd
