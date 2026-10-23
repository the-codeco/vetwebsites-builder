-- +goose Up
-- A menu section can carry its own look: the top bar's background and text colours and its size.
ALTER TABLE menus ADD COLUMN style JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE menus DROP COLUMN style;
