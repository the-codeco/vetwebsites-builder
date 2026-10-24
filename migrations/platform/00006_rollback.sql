-- +goose Up
-- A publish can put an earlier copy of the website back. restore_version names that copy; restore_of is the publish it came from.
ALTER TABLE publish_jobs
    ADD COLUMN restore_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN restore_of      BIGINT;

-- +goose Down
ALTER TABLE publish_jobs DROP COLUMN restore_version, DROP COLUMN restore_of;
