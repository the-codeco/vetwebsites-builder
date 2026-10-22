-- +goose Up
-- Site search. Posts are indexed straight from their text. A page's words live in its blocks, so the
-- app copies them into pages.search_text whenever a block changes (and once at startup for existing pages).
ALTER TABLE pages ADD COLUMN search_text TEXT NOT NULL DEFAULT '';
ALTER TABLE pages ADD COLUMN search_vec tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', title), 'A') || setweight(to_tsvector('english', search_text), 'C')
) STORED;
CREATE INDEX pages_search_idx ON pages USING GIN (search_vec);

-- the HTML tags in a post's body are not words
ALTER TABLE posts ADD COLUMN search_vec tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', title), 'A')
    || setweight(to_tsvector('english', excerpt), 'B')
    || setweight(to_tsvector('english', regexp_replace(body, '<[^>]*>', ' ', 'g')), 'C')
) STORED;
CREATE INDEX posts_search_idx ON posts USING GIN (search_vec);

-- Whether the themes show a search box in the header.
ALTER TABLE site_settings ADD COLUMN show_search BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE site_settings DROP COLUMN show_search;
DROP INDEX posts_search_idx;
ALTER TABLE posts DROP COLUMN search_vec;
DROP INDEX pages_search_idx;
ALTER TABLE pages DROP COLUMN search_vec;
ALTER TABLE pages DROP COLUMN search_text;
