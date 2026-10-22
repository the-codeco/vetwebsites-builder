-- +goose Up
-- Fonts the owner uploaded (WOFF2 files), kept in the database so backups carry them. One file per family and weight.
CREATE TABLE fonts (
    id         BIGSERIAL PRIMARY KEY,
    family     TEXT NOT NULL,
    weight     SMALLINT NOT NULL,
    data       BYTEA NOT NULL,
    hash       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (family, weight)
);

-- +goose Down
DROP TABLE fonts;
