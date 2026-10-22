-- +goose Up
-- Search and sharing settings. Pages already have a seo column; posts and the site get one too.
-- Keys: title, description, image, noindex (the site-wide row uses description, image, noindex).
ALTER TABLE posts ADD COLUMN seo JSONB NOT NULL DEFAULT '{}';
ALTER TABLE site_settings ADD COLUMN seo JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN seo;
ALTER TABLE posts DROP COLUMN seo;
