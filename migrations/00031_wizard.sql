-- +goose Up
-- The new-website wizard: how far the owner got, their answers, and the blocks and notice it made. A website that has
-- already been set up (its starting pages were created before this migration ran) is marked done, so only new sites meet it.
ALTER TABLE site_settings ADD COLUMN wizard JSONB NOT NULL DEFAULT '{}';
UPDATE site_settings SET wizard = '{"done": true}' WHERE seeded;

-- +goose Down
ALTER TABLE site_settings DROP COLUMN wizard;
