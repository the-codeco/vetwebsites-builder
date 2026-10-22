-- +goose Up
-- Earlier versions of a page's blocks, kept so a bad edit can be undone. A snapshot is taken before changes begin,
-- at most once in a while per person, and only the latest few are kept.
CREATE TABLE page_revisions (
    id         BIGSERIAL PRIMARY KEY,
    page_id    BIGINT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    made_by    TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    blocks     INT NOT NULL DEFAULT 0,
    snapshot   JSONB NOT NULL
);
CREATE INDEX page_revisions_page_idx ON page_revisions (page_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE page_revisions;
