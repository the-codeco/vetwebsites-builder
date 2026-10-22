-- +goose Up
-- Where a website is published (public_url) and the last static copy made of it. build_changes is the website's change
-- counter at that moment; a website with a larger counter has changes that are not in the copy.
ALTER TABLE sites
    ADD COLUMN public_url    TEXT NOT NULL DEFAULT '',
    ADD COLUMN build_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN build_changes BIGINT NOT NULL DEFAULT -1,
    ADD COLUMN built_at      TIMESTAMPTZ;

-- +goose Down
ALTER TABLE sites DROP COLUMN public_url, DROP COLUMN build_version, DROP COLUMN build_changes, DROP COLUMN built_at;
