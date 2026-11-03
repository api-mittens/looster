-- +goose Up
-- Validated is a terminal workflow state unless a rule explicitly targets it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=public,pg_temp AS $$
DECLARE message text; task_status text;
BEGIN
 IF NEW.task_id IS NULL THEN RETURN NEW; END IF;
 SELECT status INTO task_status FROM tasks WHERE id=NEW.task_id;
 IF task_status='validated' THEN
  IF NEW.kind<>'status_changed' OR NEW.body<>'validated' THEN RETURN NEW; END IF;
  -- Jobs from earlier events must not start after validation.
  UPDATE jobs SET state='failed',error='task validated'
   WHERE task_id=NEW.task_id AND state='pending';
  message := 'Task TSK-'||NEW.task_id||' changed (status_changed):'||E'\nvalidated';
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
   SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
   WHERE r.trigger='validated'
   ON CONFLICT DO NOTHING;
  RETURN NEW;
 END IF;
 IF NEW.kind='agent_stopped' THEN
  message := 'AFTER agent '||NEW.agent_name||' completed on task TSK-'||NEW.task_id||E'.\nSource session: '||NEW.body||E'\nReview the concrete task context and the source agent result provided with this handoff.';
 ELSE
  message := 'Task TSK-'||NEW.task_id||' changed ('||NEW.kind||'):'||E'\n'||NEW.body;
 END IF;
 IF NEW.kind<>'removed' AND task_status IS NOT NULL THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
  WHERE ((NEW.kind='status_changed' AND r.trigger=NEW.body AND r.trigger IN ('planning','todo','unvalidated','closed'))
  OR (NEW.kind='agent_stopped' AND r.trigger='completed' AND r.source=NEW.agent_name))
  AND r.target<>NEW.actor_agent
  ON CONFLICT DO NOTHING;
 END IF;
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
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Validated dispatch changes require a backup restore to roll back safely';
END; $$;
-- +goose StatementEnd
