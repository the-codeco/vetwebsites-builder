-- +goose Up
-- Things that show on every page (or chosen pages) for a while: notice bars, emergency alerts, popups, a contact button.
-- start_at and end_at are checked by the visitor's browser, because a published copy has no server to do it.
CREATE TABLE site_elements (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    enabled    BOOLEAN NOT NULL DEFAULT true,
    config     JSONB NOT NULL DEFAULT '{}',
    pages      JSONB NOT NULL DEFAULT '[]', -- addresses it shows on; empty means every page
    start_at   TIMESTAMPTZ,
    end_at     TIMESTAMPTZ,
    position   INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER site_elements_changed AFTER INSERT OR UPDATE OR DELETE ON site_elements FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();

-- +goose Down
DROP TRIGGER site_elements_changed ON site_elements;
DROP TABLE site_elements;
