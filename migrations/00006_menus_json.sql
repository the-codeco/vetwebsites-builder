-- +goose Up
-- Menus become one JSON document per section (top bar, main navigation, footer) so an
-- item can be a link, a dropdown or a mega menu, with a "sticky" (frozen) setting.
ALTER TABLE menus
    ADD COLUMN items  JSONB   NOT NULL DEFAULT '[]',
    ADD COLUMN sticky BOOLEAN NOT NULL DEFAULT false;

-- Carry every existing link over as a plain link item, in order.
UPDATE menus m SET items = COALESCE((
    SELECT jsonb_agg(jsonb_build_object('kind', 'link', 'label', i.label, 'url', i.url) ORDER BY i.position, i.id)
    FROM menu_items i WHERE i.menu_id = m.id
), '[]'::jsonb);

-- The old "header" menu is now the main navigation.
UPDATE menus SET location = 'main' WHERE location = 'header';
INSERT INTO menus (location) VALUES ('topbar'), ('main'), ('footer') ON CONFLICT (location) DO NOTHING;

DROP TABLE menu_items;

-- +goose Down
-- Plain links are restored; dropdowns, mega menus and the sticky setting are lost.
CREATE TABLE menu_items (
    id       BIGSERIAL PRIMARY KEY,
    menu_id  BIGINT NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    position INT NOT NULL,
    label    TEXT NOT NULL,
    url      TEXT NOT NULL
);
INSERT INTO menu_items (menu_id, position, label, url)
SELECT m.id, (e.ord - 1)::int, e.item->>'label', e.item->>'url'
FROM menus m, jsonb_array_elements(m.items) WITH ORDINALITY AS e(item, ord)
WHERE e.item->>'kind' = 'link';
UPDATE menus SET location = 'header' WHERE location = 'main';
DELETE FROM menus WHERE location = 'topbar';
ALTER TABLE menus DROP COLUMN items, DROP COLUMN sticky;
