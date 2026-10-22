-- +goose Up
-- How the public site treats dark mode: 'light' (no dark mode), 'toggle' (light until a visitor
-- switches) or 'auto' (follows the visitor's system setting, and they can still switch).
ALTER TABLE site_settings ADD COLUMN color_mode TEXT NOT NULL DEFAULT 'toggle'
    CHECK (color_mode IN ('light', 'toggle', 'auto'));

-- +goose Down
ALTER TABLE site_settings DROP COLUMN color_mode;
