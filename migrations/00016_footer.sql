-- +goose Up
-- The footer the owner writes: {columns: [{heading, text}], bottom}. Empty means the plain default line.
ALTER TABLE site_settings ADD COLUMN footer JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN footer;
