-- +goose Up
-- A "layout" block holds other blocks in its columns. A nested block points at its layout (parent_id) and says
-- which column it is in (slot). position now counts within one (parent, column) list; top-level blocks have no parent.
ALTER TABLE blocks
    ADD COLUMN parent_id BIGINT REFERENCES blocks (id) ON DELETE CASCADE,
    ADD COLUMN slot      SMALLINT NOT NULL DEFAULT 0;
CREATE INDEX blocks_parent_idx ON blocks (parent_id, slot, position) WHERE parent_id IS NOT NULL;

-- +goose Down
DROP INDEX blocks_parent_idx;
ALTER TABLE blocks DROP COLUMN slot, DROP COLUMN parent_id;
