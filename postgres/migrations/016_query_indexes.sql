-- +goose NO TRANSACTION
-- +goose Up
-- Evidence and fixture reproduction: docs/database-performance.md.
-- Each concurrent statement must be sent separately, outside a transaction.
-- A cancelled/failed concurrent build can leave an INVALID index with its name
-- reserved. Never CREATE INDEX IF NOT EXISTS here: it silently accepts that
-- unusable index. Restart each pair by dropping even a partially built index.
-- On retry, already successful indexes are also rebuilt. This costs I/O and
-- temporarily removes their acceleration, but never blocks normal table writes
-- for a full build or removes a constraint-backed index. Namespace migrations
-- serialize runners with their existing per-schema advisory lock.
DROP INDEX CONCURRENTLY IF EXISTS task_tags_tag_idx;
CREATE INDEX CONCURRENTLY task_tags_tag_idx ON task_tags(tag_id);
DROP INDEX CONCURRENTLY IF EXISTS memories_tag_id_idx;
CREATE INDEX CONCURRENTLY memories_tag_id_idx ON memories(tag_id,id);
DROP INDEX CONCURRENTLY IF EXISTS memories_agent_id_idx;
CREATE INDEX CONCURRENTLY memories_agent_id_idx ON memories(agent_name,id);
DROP INDEX CONCURRENTLY IF EXISTS sessions_agent_idx;
CREATE INDEX CONCURRENTLY sessions_agent_idx ON sessions(agent_name);
DROP INDEX CONCURRENTLY IF EXISTS attachments_task_id_idx;
CREATE INDEX CONCURRENTLY attachments_task_id_idx ON attachments(task_id,id);
DROP INDEX CONCURRENTLY IF EXISTS attachments_event_idx;
CREATE INDEX CONCURRENTLY attachments_event_idx ON attachments(event_id);
DROP INDEX CONCURRENTLY IF EXISTS jobs_active_session_idx;
CREATE INDEX CONCURRENTLY jobs_active_session_idx
 ON jobs(agent_name,(coalesce(task_id,context_task_id)),id)
 WHERE state IN ('pending','running','archiving');
-- Full FK-leading indexes must include terminal jobs, unlike active indexes.
DROP INDEX CONCURRENTLY IF EXISTS jobs_task_idx;
CREATE INDEX CONCURRENTLY jobs_task_idx ON jobs(task_id);
DROP INDEX CONCURRENTLY IF EXISTS jobs_agent_idx;
CREATE INDEX CONCURRENTLY jobs_agent_idx ON jobs(agent_name);
DROP INDEX CONCURRENTLY IF EXISTS jobs_schedule_rule_idx;
CREATE INDEX CONCURRENTLY jobs_schedule_rule_idx ON jobs(schedule_rule_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS jobs_schedule_rule_idx;
DROP INDEX CONCURRENTLY IF EXISTS jobs_agent_idx;
DROP INDEX CONCURRENTLY IF EXISTS jobs_task_idx;
DROP INDEX CONCURRENTLY IF EXISTS jobs_active_session_idx;
DROP INDEX CONCURRENTLY IF EXISTS attachments_event_idx;
DROP INDEX CONCURRENTLY IF EXISTS attachments_task_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS sessions_agent_idx;
DROP INDEX CONCURRENTLY IF EXISTS memories_agent_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS memories_tag_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS task_tags_tag_idx;
