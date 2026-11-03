-- +goose Up
ALTER TABLE tags ADD COLUMN manifest_active boolean NOT NULL DEFAULT false;
ALTER TABLE rules ADD COLUMN tag_pattern text NOT NULL DEFAULT '';
ALTER TABLE rules ADD COLUMN tag_negated boolean NOT NULL DEFAULT false;
ALTER TABLE rules ADD CONSTRAINT rules_tag_condition_check CHECK (
 (tag_pattern='' AND NOT tag_negated) OR
 (tag_pattern<>'' AND tag_pattern ~ '^[A-Za-z*/-]+$' AND trigger<>'every')
);
CREATE TABLE behaviour_manifests (
 id bigserial PRIMARY KEY,
 source text NOT NULL,
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array'),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE behaviour_manifest_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 active_id bigint NOT NULL REFERENCES behaviour_manifests
);
-- Preserve pre-manifest installations as a generated, readable first version.
WITH definitions AS (
 SELECT 0 AS ordering,name AS sort_key,
  jsonb_build_object('trigger','agent','target',name,'prompt',description) AS rule,
  'AGENT '||name||' WITH '||to_json(description)::text||';' AS line FROM agents
 UNION ALL
 SELECT 1,name,jsonb_build_object('trigger','tag','target',name,'prompt',description),
  'TAG '||name||' WITH '||to_json(description)::text||';' FROM tags
 UNION ALL
 SELECT 2,lpad(id::text,20,'0'),
  jsonb_build_object('trigger',trigger,'source',coalesce(source,''),'target',target,'interval_seconds',interval_seconds,'prompt',prompt,'tag_pattern','','tag_negated',false),
  CASE WHEN trigger='every' THEN 'EVERY '||interval_seconds||' SECONDS PING '||target||' WITH '||to_json(prompt)::text
   WHEN trigger='completed' THEN 'AFTER '||source||' COMPLETED PING '||target
   WHEN trigger LIKE 'stop_%' THEN 'AFTER '||upper(substr(trigger,6))||' STOP PINGING '||target
   ELSE 'ON '||upper(trigger)||' PING '||target END||';' FROM rules
), initial AS (
 INSERT INTO behaviour_manifests(source,rules)
 SELECT '-- Generated from existing database configuration.'||E'\n'||coalesce(string_agg(line,E'\n' ORDER BY ordering,sort_key),'')||E'\n',
 coalesce(jsonb_agg(rule ORDER BY ordering,sort_key),'[]'::jsonb) FROM definitions RETURNING id
)
INSERT INTO behaviour_manifest_state(active_id) SELECT id FROM initial;
UPDATE tags SET manifest_active=true;
-- +goose StatementBegin
CREATE FUNCTION looster_immutable_manifest() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Behaviour manifest versions are immutable';
END; $$;
CREATE TRIGGER immutable_manifest BEFORE UPDATE OR DELETE ON behaviour_manifests
 FOR EACH ROW EXECUTE FUNCTION looster_immutable_manifest();

-- Wildcards are glob '*', not SQL LIKE '%' or '_'. Validated patterns contain
-- only tag-name characters, slash, and star, so regex metacharacters are absent.
CREATE FUNCTION looster_rule_matches(p_task_id bigint,p_pattern text,p_negated boolean)
RETURNS boolean LANGUAGE sql STABLE SECURITY INVOKER AS $$
 SELECT CASE WHEN p_pattern='' THEN true ELSE
  EXISTS(SELECT FROM task_tags tt JOIN tags t ON t.id=tt.tag_id
   WHERE tt.task_id=p_task_id AND t.manifest_active
    AND t.name ~ ('^'||replace(p_pattern,'*','.*')||'$')) <> p_negated END;
$$;
CREATE OR REPLACE FUNCTION looster_ping_stopped(p_task_id bigint,p_agent_name text,p_event_id bigint DEFAULT NULL)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY INVOKER AS $$
 SELECT EXISTS(
  SELECT FROM rules r JOIN events v ON v.task_id=p_task_id
  WHERE r.trigger='stop_'||CASE WHEN v.body='unvalidated' THEN 'todo' ELSE v.body END AND r.target=p_agent_name
   AND v.kind='status_changed' AND (p_event_id IS NULL OR v.id<=p_event_id)
   AND looster_rule_matches(p_task_id,r.tag_pattern,r.tag_negated)
 );
$$;
-- Replacing a function retains its previous configuration, explicitly remove
-- the historical public-only search_path for schema-per-namespace execution.
ALTER FUNCTION looster_ping_stopped(bigint,text,bigint) RESET search_path;
CREATE OR REPLACE FUNCTION looster_dispatch() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE message text;
BEGIN
 IF NEW.task_id IS NULL THEN RETURN NEW; END IF;
 IF NEW.kind='agent_stopped' THEN
  message := 'AFTER agent '||NEW.agent_name||' completed on task TSK-'||NEW.task_id||E'.\nSource session: '||NEW.body||E'\nReview the concrete task context and the source agent result provided with this handoff.';
 ELSE
  message := 'Task TSK-'||NEW.task_id||' changed ('||NEW.kind||'):'||E'\n'||NEW.body;
 END IF;
 IF NEW.kind IN ('status_changed','tags_changed') THEN
  UPDATE jobs j SET state='failed',error='ping stopped by behaviour'
   WHERE j.task_id=NEW.task_id AND j.state='pending'
    AND looster_ping_stopped(NEW.task_id,j.agent_name,NEW.id);
 END IF;
 IF NEW.kind<>'removed' AND EXISTS(SELECT FROM tasks WHERE id=NEW.task_id) THEN
  INSERT INTO jobs(task_id,agent_name,event_id,prompt)
  SELECT NEW.task_id,r.target,NEW.id,message FROM rules r
  WHERE ((NEW.kind='status_changed' AND r.trigger=NEW.body AND r.trigger IN ('planning','todo','validated','closed'))
  OR (NEW.kind='agent_stopped' AND r.trigger='completed' AND r.source=NEW.agent_name))
  AND r.target<>NEW.actor_agent
  AND looster_rule_matches(NEW.task_id,r.tag_pattern,r.tag_negated)
  AND NOT looster_ping_stopped(NEW.task_id,r.target,NEW.id)
  ON CONFLICT DO NOTHING;
 END IF;
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
ALTER FUNCTION looster_dispatch() RESET search_path;
-- CreateTask inserts its status event after assigning the complete tag set.
-- Direct SQL inserts retain the normal immediate entry event.
CREATE OR REPLACE FUNCTION looster_task_event() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN
 IF TG_OP='DELETE' THEN RETURN OLD;
 ELSIF TG_OP='INSERT' THEN
  INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'created',NEW.name);
  IF current_setting('looster.defer_task_status',true) IS DISTINCT FROM 'true' THEN
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
  END IF;
 ELSIF NEW IS DISTINCT FROM OLD THEN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'status_changed',NEW.status);
  ELSE
   INSERT INTO events(task_id,kind,body) VALUES(NEW.id,'updated',NEW.name||E'\n'||NEW.description);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
ALTER FUNCTION looster_task_event() RESET search_path;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Versioned manifests require restoring a backup to roll back safely';
END; $$;
-- +goose StatementEnd
