-- +goose Up
ALTER TABLE media
    ADD COLUMN original_name TEXT   NOT NULL DEFAULT '',
    ADD COLUMN width         INT    NOT NULL DEFAULT 0,
    ADD COLUMN height        INT    NOT NULL DEFAULT 0,
    ADD COLUMN thumb_path    TEXT   NOT NULL DEFAULT '',
    ADD COLUMN uploaded_by   BIGINT REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE media
    DROP COLUMN original_name, DROP COLUMN width, DROP COLUMN height,
    DROP COLUMN thumb_path, DROP COLUMN uploaded_by;
