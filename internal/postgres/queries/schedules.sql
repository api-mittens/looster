-- name: ScheduleDue :one
SELECT looster_schedule(sqlc.arg(agents)::text[])::integer AS dispatched;
