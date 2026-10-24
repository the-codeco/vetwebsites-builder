-- +goose Up
-- What a website may hold. NULL means the server's default (QUOTA_STORAGE_MB, QUOTA_PAGES, QUOTA_POSTS); 0 means no
-- limit; anything else is the limit. A platform admin sets these per website.
ALTER TABLE sites
    ADD COLUMN quota_storage_mb INT CHECK (quota_storage_mb >= 0),
    ADD COLUMN quota_pages      INT CHECK (quota_pages >= 0),
    ADD COLUMN quota_posts      INT CHECK (quota_posts >= 0);

-- +goose Down
ALTER TABLE sites DROP COLUMN quota_storage_mb, DROP COLUMN quota_pages, DROP COLUMN quota_posts;
