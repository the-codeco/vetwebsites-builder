-- +goose Up
ALTER TABLE posts ADD COLUMN cover_image TEXT NOT NULL DEFAULT '';
CREATE INDEX posts_published_idx ON posts (published_at DESC) WHERE status = 'published';
CREATE INDEX post_categories_category_idx ON post_categories (category_id);

-- +goose Down
DROP INDEX post_categories_category_idx;
DROP INDEX posts_published_idx;
ALTER TABLE posts DROP COLUMN cover_image;
