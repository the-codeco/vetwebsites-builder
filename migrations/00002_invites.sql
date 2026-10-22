-- +goose Up
CREATE TABLE invites (
    token_hash BYTEA PRIMARY KEY,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'editor')),
    invited_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE invites;
