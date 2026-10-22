-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'editor')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- single-row table
CREATE TABLE site_settings (
    id           SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    title        TEXT NOT NULL DEFAULT 'My Site',
    domain       TEXT NOT NULL DEFAULT '',
    active_theme TEXT NOT NULL DEFAULT 'default',
    tokens       JSONB NOT NULL DEFAULT '{}',
    custom_css   TEXT NOT NULL DEFAULT ''
);
INSERT INTO site_settings (id) VALUES (1);

CREATE TABLE pages (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    title      TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    seo        JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blocks (
    id       BIGSERIAL PRIMARY KEY,
    page_id  BIGINT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    position INT NOT NULL,
    type     TEXT NOT NULL,
    props    JSONB NOT NULL DEFAULT '{}',
    style    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX blocks_page_idx ON blocks (page_id, position);

CREATE TABLE categories (
    id   BIGSERIAL PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE posts (
    id           BIGSERIAL PRIMARY KEY,
    slug         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    excerpt      TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    author_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    published_at TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE post_categories (
    post_id     BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    category_id BIGINT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, category_id)
);

CREATE TABLE menus (
    id       BIGSERIAL PRIMARY KEY,
    location TEXT NOT NULL UNIQUE
);

CREATE TABLE menu_items (
    id       BIGSERIAL PRIMARY KEY,
    menu_id  BIGINT NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    position INT NOT NULL,
    label    TEXT NOT NULL,
    url      TEXT NOT NULL
);

CREATE TABLE media (
    id         BIGSERIAL PRIMARY KEY,
    path       TEXT NOT NULL UNIQUE,
    mime       TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    alt        TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE media, menu_items, menus, post_categories, posts, categories,
           blocks, pages, site_settings, sessions, users;
