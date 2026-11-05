-- +goose Up
-- Nullable intentionally: pre-migration output has no provable run identity.
-- No foreign key, so output survives mutable job removal.
ALTER TABLE events ADD COLUMN job_id bigint;
CREATE INDEX events_job_id_idx ON events(job_id,id) WHERE job_id IS NOT NULL;

-- +goose Down
DROP INDEX events_job_id_idx;
ALTER TABLE events DROP COLUMN job_id;
