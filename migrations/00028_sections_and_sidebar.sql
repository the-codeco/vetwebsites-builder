-- +goose Up
-- (IF NOT EXISTS: development databases ran this before it was renumbered from 00026.)
-- A page can belong under a top-level page (one level deep), in an order of its own: the sidebar's section menu
-- lists a section's pages. sidebar: whether this page shows the site's sidebar. site_settings.sidebar: its widgets.
ALTER TABLE pages
    ADD COLUMN IF NOT EXISTS parent_id BIGINT REFERENCES pages(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS position  INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sidebar   BOOLEAN NOT NULL DEFAULT true;
CREATE INDEX IF NOT EXISTS pages_parent_idx ON pages (parent_id);
ALTER TABLE site_settings ADD COLUMN IF NOT EXISTS sidebar JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN sidebar;
DROP INDEX pages_parent_idx;
ALTER TABLE pages DROP COLUMN parent_id, DROP COLUMN position, DROP COLUMN sidebar;
