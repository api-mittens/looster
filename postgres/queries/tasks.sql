-- name: GetTask :one
SELECT tasks.id, tasks.name, tasks.description, tasks.status, tasks.created_at,
 COALESCE((SELECT max(e.created_at) FROM events e WHERE e.task_id=tasks.id), tasks.created_at)::timestamptz AS last_activity_at
FROM tasks WHERE tasks.id = $1;

-- name: ListTasks :many
SELECT tasks.id, tasks.name, tasks.description, tasks.status, tasks.created_at,
 ARRAY(SELECT tags.name FROM tags JOIN task_tags ON tags.id=task_tags.tag_id
       WHERE task_tags.task_id=tasks.id ORDER BY tags.name)::text[] AS tags,
 activity.status_changes, activity.comment_count,
 COALESCE((SELECT max(e.created_at) FROM events e WHERE e.task_id=tasks.id), tasks.created_at)::timestamptz AS last_activity_at
FROM tasks
LEFT JOIN LATERAL (
 SELECT count(*) FILTER (WHERE kind='status_changed') AS status_changes,
        count(*) FILTER (WHERE kind='comment') AS comment_count
 FROM events WHERE task_id=tasks.id AND kind IN ('status_changed','comment')
) activity ON true
ORDER BY tasks.id;

-- name: CreateTask :one
INSERT INTO tasks(name, description, status) VALUES ($1,$2,$3) RETURNING id;

-- name: SetTaskStatus :execrows
UPDATE tasks SET status=$2 WHERE id=$1;

-- name: RemoveTask :execrows
DELETE FROM tasks WHERE id=$1;
