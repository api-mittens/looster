-- +goose Up
-- Manual mentions are event-driven, so every comment entry point shares the
-- same transactional dispatch. Run before dispatch's ordinary notifications:
-- the existing (event_id,agent_name) key gives a mentioned session one real job,
-- never both a manual ping and a notification.
-- +goose StatementBegin
CREATE FUNCTION looster_comment_mentions() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF NEW.kind='comment' AND NEW.actor_agent='' AND NEW.agent_name='' THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,a.name,NEW.id,
   'You were mentioned in a human comment on task TSK-'||NEW.task_id||E':\n'||NEW.body
  FROM agents a JOIN tasks t ON t.id=NEW.task_id
  WHERE t.status<>'closed'
   AND a.name IN (
    SELECT m[2] FROM regexp_matches(NEW.body,'(^|[^A-Za-z0-9_@-])@([A-Za-z][A-Za-z0-9_-]*)','g') AS m
   )
   AND NOT looster_ping_stopped(NEW.task_id,a.name,NEW.id)
  ON CONFLICT DO NOTHING;
 END IF;
 RETURN NEW;
END; $$;
-- Pin the namespace exactly as migration 014 does, including direct SQL callers.
DO $$ BEGIN
 EXECUTE format('ALTER FUNCTION %I.looster_comment_mentions() SET search_path TO %I, pg_temp',current_schema(),current_schema());
END; $$;
CREATE TRIGGER comment_mentions AFTER INSERT ON events FOR EACH ROW EXECUTE FUNCTION looster_comment_mentions();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER comment_mentions ON events;
DROP FUNCTION looster_comment_mentions();
