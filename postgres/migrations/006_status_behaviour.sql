-- +goose Up
-- Agent PROMPT declarations use the existing agents table; this migration adds
-- status-entry triggers and makes AFTER dispatch context explicit.
ALTER TABLE rules DROP CONSTRAINT rules_trigger_check;
ALTER TABLE rules ADD CONSTRAINT rules_trigger_check CHECK(trigger IN ('planning','todo','unvalidated','validated','completed','every'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
DECLARE message text;
BEGIN
 IF NEW.task_id IS NULL THEN RETURN NEW; END IF;
 IF NEW.kind='agent_stopped' THEN
  message := 'AFTER agent '||NEW.agent_name||' completed on task TSK-'||NEW.task_id||E'.\nSource session: '||NEW.body||E'\nReview the concrete task context and the source agent result provided with this handoff.';
 ELSE
  message := 'Task TSK-'||NEW.task_id||' changed ('||NEW.kind||'):'||E'\n'||NEW.body;
 END IF;
 -- Workflow pings take precedence if the same event also notifies a session.
 IF NEW.kind<>'removed' AND EXISTS(SELECT FROM tasks WHERE id=NEW.task_id) THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
  WHERE ((NEW.kind='status_changed' AND r.trigger=NEW.body AND r.trigger IN ('planning','todo','unvalidated','validated'))
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
  INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
 ELSIF NEW IS DISTINCT FROM OLD THEN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
  ELSE
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'updated',NEW.name||E'\n'||NEW.description);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM rules WHERE trigger IN ('planning','unvalidated','validated');
ALTER TABLE rules DROP CONSTRAINT rules_trigger_check;
ALTER TABLE rules ADD CONSTRAINT rules_trigger_check CHECK(trigger IN ('todo','completed','every'));
-- +goose StatementBegin
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
-- +goose StatementEnd
