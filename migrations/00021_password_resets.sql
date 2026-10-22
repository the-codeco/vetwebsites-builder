-- +goose Up
-- One-time links for choosing a new password. Only a hash of the token is kept, and a user has at most one link at a time.
CREATE TABLE password_resets (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE password_resets;
