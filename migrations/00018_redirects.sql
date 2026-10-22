-- +goose Up
-- Old addresses that send visitors somewhere else: made by hand, or automatically when a page or post changes address.
CREATE TABLE redirects (
    id         BIGSERIAL PRIMARY KEY,
    from_path  TEXT NOT NULL UNIQUE,
    to_url     TEXT NOT NULL,
    permanent  BOOLEAN NOT NULL DEFAULT true,
    auto       BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE redirects;
