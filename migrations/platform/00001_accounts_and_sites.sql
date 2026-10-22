-- +goose Up
-- Accounts. The same shape the single-site program uses, so the login, session, invite and password reset code is shared.
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'customer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- acting_site_id is set while a platform admin is working on someone's site.
CREATE TABLE sessions (
    token_hash     BYTEA PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token     TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    acting_site_id BIGINT
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE invites (
    token_hash BYTEA PRIMARY KEY,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'customer')),
    invited_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE password_resets (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One website per customer. Its content lives in its own schema (schema_name); public_token identifies the site to the
-- forms and beacons on its published pages.
CREATE TABLE sites (
    id           BIGSERIAL PRIMARY KEY,
    owner_id     BIGINT UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    name         TEXT NOT NULL,
    schema_name  TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    public_token TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE sessions ADD CONSTRAINT sessions_acting_site_fk FOREIGN KEY (acting_site_id) REFERENCES sites(id) ON DELETE SET NULL;

-- Who did what to whose site: logging in as a customer, creating, suspending and deleting sites, publishing.
CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor      TEXT NOT NULL,
    site_id    BIGINT,
    site_name  TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC, id DESC);

-- +goose Down
DROP TABLE audit_log;
ALTER TABLE sessions DROP CONSTRAINT sessions_acting_site_fk;
DROP TABLE sites;
DROP TABLE password_resets;
DROP TABLE invites;
DROP TABLE sessions;
DROP TABLE users;
