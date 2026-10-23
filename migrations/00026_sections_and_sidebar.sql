-- +goose Up
-- A page can belong under a top-level page (one level deep), in an order of its own: the sidebar's section menu
-- lists a section's pages. sidebar: whether this page shows the site's sidebar. site_settings.sidebar: its widgets.
ALTER TABLE pages
    ADD COLUMN parent_id BIGINT REFERENCES pages(id) ON DELETE SET NULL,
    ADD COLUMN position  INT NOT NULL DEFAULT 0,
    ADD COLUMN sidebar   BOOLEAN NOT NULL DEFAULT true;
CREATE INDEX pages_parent_idx ON pages (parent_id);
ALTER TABLE site_settings ADD COLUMN sidebar JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN sidebar;
DROP INDEX pages_parent_idx;
ALTER TABLE pages DROP COLUMN parent_id, DROP COLUMN position, DROP COLUMN sidebar;
