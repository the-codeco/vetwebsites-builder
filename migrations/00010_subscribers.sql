-- +goose Up
-- Newsletter subscribers. Only the address and when it was given are kept. The confirm /
-- unsubscribe link holds a random token; only its hash is stored here.
CREATE TABLE subscribers (
    id           BIGSERIAL PRIMARY KEY,
    email        TEXT NOT NULL UNIQUE, -- always lower case
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ,          -- NULL until the address owner confirms by email
    token_hash   TEXT NOT NULL UNIQUE
);
CREATE INDEX subscribers_created_idx ON subscribers (created_at DESC);

-- +goose Down
DROP TABLE subscribers;
