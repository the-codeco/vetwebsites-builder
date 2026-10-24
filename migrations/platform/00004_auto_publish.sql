-- +goose Up
-- A website can be told to publish itself when scheduled pages and posts go live (they would otherwise only be live in
-- the editor until someone pressed Publish).
ALTER TABLE publish_targets ADD COLUMN auto_publish BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE publish_targets DROP COLUMN auto_publish;
