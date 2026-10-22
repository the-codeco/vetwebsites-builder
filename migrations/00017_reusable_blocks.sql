-- +goose Up
-- Blocks written once and used on many pages. A page holds a "reusable" block whose props say {"ref": id};
-- what the visitor sees is the block saved here.
CREATE TABLE reusable_blocks (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL,
    props      JSONB NOT NULL DEFAULT '{}',
    style      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE reusable_blocks;
