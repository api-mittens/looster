-- +goose Up
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK(status IN ('planning','todo','unvalidated','validated','closed'));
ALTER TABLE rules DROP CONSTRAINT rules_trigger_check;
ALTER TABLE rules ADD CONSTRAINT rules_trigger_check CHECK(trigger IN ('planning','todo','unvalidated','validated','closed','completed','every'));
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
  WHERE ((NEW.kind='status_changed' AND r.trigger=NEW.body AND r.trigger IN ('planning','todo','unvalidated','validated','closed'))
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
-- +goose StatementEnd

-- +goose Down
DELETE FROM rules WHERE trigger='closed';
UPDATE tasks SET status='validated' WHERE status='closed';
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK(status IN ('planning','todo','unvalidated','validated'));
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
-- +goose StatementEnd
