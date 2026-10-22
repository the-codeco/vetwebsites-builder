-- +goose Up
ALTER TABLE site_settings ADD COLUMN seeded BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE site_settings DROP COLUMN seeded;
