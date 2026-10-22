-- +goose Up
-- A draft with a publish_at in the future is published automatically when that time arrives.
ALTER TABLE pages ADD COLUMN publish_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN publish_at TIMESTAMPTZ;
CREATE INDEX pages_due_idx ON pages (publish_at) WHERE publish_at IS NOT NULL;
CREATE INDEX posts_due_idx ON posts (publish_at) WHERE publish_at IS NOT NULL;

-- +goose Down
DROP INDEX pages_due_idx;
DROP INDEX posts_due_idx;
ALTER TABLE pages DROP COLUMN publish_at;
ALTER TABLE posts DROP COLUMN publish_at;
