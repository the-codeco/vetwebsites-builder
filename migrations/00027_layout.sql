-- +goose Up
-- (IF NOT EXISTS: development databases ran this before it was renumbered from 00025.)
-- layout: how the page is framed (full width or a centred box, the page background) and which header the site has.
ALTER TABLE site_settings ADD COLUMN IF NOT EXISTS layout JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN layout;
