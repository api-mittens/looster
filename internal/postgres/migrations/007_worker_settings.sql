-- +goose Up
ALTER TABLE workers ADD COLUMN settings jsonb;

-- +goose Down
ALTER TABLE workers DROP COLUMN settings;
