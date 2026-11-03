-- name: RemoveAbsentRules :exec
DELETE FROM rules r WHERE NOT EXISTS (
 SELECT FROM jsonb_to_recordset(sqlc.arg(rules)::jsonb)
 AS d(trigger text,source text,target text,interval_seconds bigint,prompt text,tag_pattern text,tag_negated boolean)
 WHERE d.trigger=r.trigger AND nullif(d.source,'') IS NOT DISTINCT FROM r.source
 AND d.target=r.target AND coalesce(d.interval_seconds,0)=r.interval_seconds
 AND coalesce(d.prompt,'')=r.prompt AND coalesce(d.tag_pattern,'')=r.tag_pattern
 AND coalesce(d.tag_negated,false)=r.tag_negated
);

-- name: AddNewRules :exec
INSERT INTO rules(trigger,source,target,interval_seconds,prompt,tag_pattern,tag_negated,next_run_at)
SELECT d.trigger,nullif(d.source,''),d.target,coalesce(d.interval_seconds,0),coalesce(d.prompt,''),coalesce(d.tag_pattern,''),coalesce(d.tag_negated,false),
 CASE WHEN d.trigger='every' THEN statement_timestamp()+d.interval_seconds*interval '1 second' ELSE NULL END
FROM jsonb_to_recordset(sqlc.arg(rules)::jsonb)
 AS d(trigger text,source text,target text,interval_seconds bigint,prompt text,tag_pattern text,tag_negated boolean)
WHERE d.trigger NOT IN ('prompt','agent','tag') AND NOT EXISTS (
 SELECT FROM rules r WHERE d.trigger=r.trigger
 AND nullif(d.source,'') IS NOT DISTINCT FROM r.source
 AND d.target=r.target AND coalesce(d.interval_seconds,0)=r.interval_seconds
 AND coalesce(d.prompt,'')=r.prompt AND coalesce(d.tag_pattern,'')=r.tag_pattern
 AND coalesce(d.tag_negated,false)=r.tag_negated
);

-- name: UpsertPromptAgents :exec
INSERT INTO agents(name,description)
SELECT DISTINCT d.target,coalesce(d.prompt,'') FROM jsonb_to_recordset(sqlc.arg(statements)::jsonb)
 AS d(trigger text,target text,prompt text)
WHERE d.trigger IN ('prompt','agent')
ON CONFLICT(name) DO UPDATE SET description=excluded.description;
