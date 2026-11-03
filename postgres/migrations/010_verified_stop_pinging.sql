-- +goose Up
ALTER TABLE rules DROP CONSTRAINT rules_trigger_check;
ALTER TABLE rules ADD CONSTRAINT rules_trigger_check CHECK(trigger IN ('planning','todo','unvalidated','validated','closed','completed','every','stop_planning','stop_todo','stop_unvalidated','stop_validated','stop_closed'));
-- +goose StatementBegin
CREATE FUNCTION looster_ping_stopped(p_task_id bigint, p_agent_name text, p_event_id bigint DEFAULT NULL)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY INVOKER SET search_path=public,pg_temp AS $$
 SELECT EXISTS(
  SELECT FROM rules r JOIN events v ON v.task_id=p_task_id
  WHERE r.trigger='stop_'||v.body AND r.target=p_agent_name
   AND v.kind='status_changed'
   AND (p_event_id IS NULL OR v.id<=p_event_id)
 );
$$;
CREATE OR REPLACE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
DECLARE message text;
BEGIN
 IF NEW.task_id IS NULL THEN RETURN NEW; END IF;
 IF NEW.kind='agent_stopped' THEN
  message := 'AFTER agent '||NEW.agent_name||' completed on task TSK-'||NEW.task_id||E'.\nSource session: '||NEW.body||E'\nReview the concrete task context and the source agent result provided with this handoff.';
 ELSE
  message := 'Task TSK-'||NEW.task_id||' changed ('||NEW.kind||'):'||E'\n'||NEW.body;
 END IF;
 IF NEW.kind='status_changed' THEN
  UPDATE jobs j SET state='failed',error='ping stopped by behaviour'
   WHERE j.task_id=NEW.task_id AND j.state='pending'
    AND looster_ping_stopped(NEW.task_id,j.agent_name,NEW.id);
 END IF;
 -- Workflow pings take precedence if the same event also notifies a session.
 IF NEW.kind<>'removed' AND EXISTS(SELECT FROM tasks WHERE id=NEW.task_id) THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
  WHERE ((NEW.kind='status_changed' AND r.trigger=NEW.body AND r.trigger IN ('planning','todo','unvalidated','validated','closed'))
  OR (NEW.kind='agent_stopped' AND r.trigger='completed' AND r.source=NEW.agent_name))
  AND r.target<>NEW.actor_agent
  AND NOT looster_ping_stopped(NEW.task_id,r.target,NEW.id)
  ON CONFLICT DO NOTHING;
 END IF;
 -- Never fan out lifecycle/output/notification-delivery events.
 IF NEW.kind IN ('updated','status_changed','comment','tags_changed','attachment_added','attachment_removed','attachment_updated','removed') THEN
  IF NEW.kind='removed' THEN
   INSERT INTO jobs(context_task_id,agent_name,event_id,prompt,session_id,notification)
   SELECT NEW.task_id,s.agent_name,NEW.id,message||E'\nThis task has been deleted; do not continue work on it.',s.session_id,true
   FROM sessions s WHERE s.task_id=NEW.task_id AND s.session_id<>'' AND s.agent_name<>NEW.actor_agent
    AND NOT looster_ping_stopped(NEW.task_id,s.agent_name,NEW.id)
   ON CONFLICT DO NOTHING;
  ELSE
   INSERT INTO jobs(task_id,agent_name,event_id,prompt,notification)
   SELECT NEW.task_id,s.agent_name,NEW.id,message,true FROM sessions s WHERE s.task_id=NEW.task_id AND s.agent_name<>NEW.actor_agent
    AND NOT looster_ping_stopped(NEW.task_id,s.agent_name,NEW.id)
   ON CONFLICT DO NOTHING;
  END IF;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Verified stop rules require a backup restore to roll back safely';
END; $$;
-- +goose StatementEnd
