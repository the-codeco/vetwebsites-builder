-- +goose Up
-- The cookie notice shown on the public site: {enabled, ask, text, link_label, link_url}. Empty means off.
ALTER TABLE site_settings ADD COLUMN consent JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN consent;
